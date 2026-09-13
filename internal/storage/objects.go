package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	multipartPartSize    = 16 * 1024 * 1024
	multipartConcurrency = 4
	maxMultipartParts    = 10_000
)

var (
	ErrMissing       = errors.New("storage object is missing")
	ErrExists        = errors.New("storage object already exists")
	ErrTemporary     = errors.New("storage operation failed temporarily")
	ErrConfiguration = errors.New("storage configuration is invalid")
)

type ByteRange struct {
	Offset int64
	Length int64
}

type Object struct {
	Key    string
	Size   int64
	SHA256 [sha256.Size]byte
}

type ReadObject struct {
	Body       io.ReadCloser
	Size       int64
	ObjectSize int64
}

type ObjectStore interface {
	Put(context.Context, string, io.Reader, int64) (Object, error)
	Open(context.Context, string, *ByteRange) (ReadObject, error)
	Stat(context.Context, string) (Object, error)
	Delete(context.Context, string) error
	Close() error
}

type localStore struct{ root *os.Root }

func NewLocalStore(root string) (ObjectStore, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open local storage root: %w", err)
	}
	return &localStore{root: opened}, nil
}

func (s *localStore) Close() error { return s.root.Close() }

func (s *localStore) Put(ctx context.Context, key string, body io.Reader, size int64) (Object, error) {
	if err := validateObjectKey(key); err != nil {
		return Object{}, err
	}
	if size < 0 {
		return Object{}, fmt.Errorf("object size must not be negative: %w", ErrConfiguration)
	}
	directory := path.Dir(key)
	if directory != "." {
		if err := s.root.MkdirAll(directory, 0o700); err != nil {
			return Object{}, classifyLocal("create object directory", err)
		}
	}
	temporary := key + ".tmp-" + rand.Text()
	file, err := s.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Object{}, classifyLocal("create temporary object", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), &contextReader{ctx: ctx, reader: io.LimitReader(body, size+1)})
	closeErr := errors.Join(file.Sync(), file.Close())
	if copyErr != nil || closeErr != nil || written != size {
		_ = s.root.Remove(temporary)
		if copyErr != nil || closeErr != nil {
			return Object{}, classifyLocal("write temporary object", errors.Join(copyErr, closeErr))
		}
		return Object{}, fmt.Errorf("write object: got %d bytes, want %d: %w", written, size, ErrConfiguration)
	}
	if err := s.root.Link(temporary, key); err != nil {
		_ = s.root.Remove(temporary)
		if errors.Is(err, fs.ErrExist) {
			return Object{}, fmt.Errorf("finalize object: %w", ErrExists)
		}
		return Object{}, classifyLocal("finalize object", err)
	}
	if err := s.root.Remove(temporary); err != nil {
		return Object{}, classifyLocal("remove temporary object", err)
	}
	directoryHandle, err := s.root.Open(directory)
	if err != nil {
		return Object{}, classifyLocal("open object directory", err)
	}
	if err := errors.Join(directoryHandle.Sync(), directoryHandle.Close()); err != nil {
		return Object{}, classifyLocal("sync object directory", err)
	}
	var checksum [sha256.Size]byte
	copy(checksum[:], hash.Sum(nil))
	return Object{Key: key, Size: written, SHA256: checksum}, nil
}

func (s *localStore) Open(ctx context.Context, key string, byteRange *ByteRange) (ReadObject, error) {
	if err := validateObjectKey(key); err != nil {
		return ReadObject{}, err
	}
	file, err := s.root.Open(key)
	if err != nil {
		return ReadObject{}, classifyLocal("open object", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return ReadObject{}, classifyLocal("stat object", err)
	}
	offset, length, err := resolveRange(info.Size(), byteRange)
	if err != nil {
		_ = file.Close()
		return ReadObject{}, err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return ReadObject{}, classifyLocal("seek object", err)
	}
	body := io.Reader(file)
	if byteRange != nil {
		body = io.LimitReader(file, length)
	}
	return ReadObject{Body: &readCloser{Reader: &contextReader{ctx: ctx, reader: body}, close: file.Close}, Size: length, ObjectSize: info.Size()}, nil
}

func (s *localStore) Stat(ctx context.Context, key string) (Object, error) {
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}
	if err := validateObjectKey(key); err != nil {
		return Object{}, err
	}
	info, err := s.root.Stat(key)
	if err != nil {
		return Object{}, classifyLocal("stat object", err)
	}
	if !info.Mode().IsRegular() {
		return Object{}, fmt.Errorf("object is not a regular file: %w", ErrConfiguration)
	}
	return Object{Key: key, Size: info.Size()}, nil
}

func (s *localStore) Delete(_ context.Context, key string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	if err := s.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return classifyLocal("delete object", err)
	}
	return nil
}

type s3Store struct {
	client *awss3.Client
	bucket string
	prefix string
}

func NewS3Store(config S3Config, accessKeyID, secretAccessKey string) ObjectStore {
	return &s3Store{
		client: newS3Client(config, s3Credentials{AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey}),
		bucket: config.Bucket, prefix: config.Prefix,
	}
}

func (*s3Store) Close() error { return nil }

func (s *s3Store) Put(ctx context.Context, key string, body io.Reader, size int64) (Object, error) {
	if err := validateObjectKey(key); err != nil {
		return Object{}, err
	}
	if size < 0 {
		return Object{}, fmt.Errorf("object size must not be negative: %w", ErrConfiguration)
	}
	if size <= multipartPartSize {
		return s.putSingle(ctx, key, body, size)
	}
	return s.putMultipart(ctx, key, body, size)
}

func (s *s3Store) putSingle(ctx context.Context, key string, body io.Reader, size int64) (Object, error) {
	content := make([]byte, size)
	reader := &exactReader{reader: &contextReader{ctx: ctx, reader: body}, remaining: size}
	if _, err := io.ReadFull(reader, content); err != nil {
		return Object{}, fmt.Errorf("read object body: %w: %w", ErrConfiguration, err)
	}
	if err := reader.complete(); err != nil {
		return Object{}, err
	}
	_, err := s.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(s.key(key)), Body: bytes.NewReader(content), ContentLength: aws.Int64(size),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		return Object{}, classifyS3("put object", err)
	}
	checksum := sha256.Sum256(content)
	return Object{Key: key, Size: size, SHA256: checksum}, nil
}

func (s *s3Store) putMultipart(ctx context.Context, key string, body io.Reader, size int64) (object Object, resultErr error) {
	objectKey := s.key(key)
	created, err := s.client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(s.bucket), Key: aws.String(objectKey),
	})
	if err != nil {
		return Object{}, classifyS3("start multipart upload", err)
	}
	defer func() {
		if resultErr != nil {
			abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_, _ = s.client.AbortMultipartUpload(abortCtx, &awss3.AbortMultipartUploadInput{
				Bucket: aws.String(s.bucket), Key: aws.String(objectKey), UploadId: created.UploadId,
			})
		}
	}()
	uploadCtx, cancelUploads := context.WithCancel(ctx)
	defer cancelUploads()
	hash := sha256.New()
	reader := &exactReader{reader: io.TeeReader(&contextReader{ctx: uploadCtx, reader: body}, hash), remaining: size}
	partSize := max(int64(multipartPartSize), (size+maxMultipartParts-1)/maxMultipartParts)
	partCount := int((size + partSize - 1) / partSize)
	parts := make([]types.CompletedPart, partCount)
	semaphore := make(chan struct{}, multipartConcurrency)
	var wait sync.WaitGroup
	var errorMu sync.Mutex
	var uploadErr error
	var readErr error

readParts:
	for index := range partCount {
		select {
		case semaphore <- struct{}{}:
		case <-uploadCtx.Done():
			readErr = uploadCtx.Err()
			break readParts
		}
		length := min(partSize, reader.remaining)
		content := make([]byte, length)
		if _, err := io.ReadFull(reader, content); err != nil {
			<-semaphore
			readErr = err
			break
		}
		partNumber := int32(index + 1)
		wait.Go(func() {
			defer func() { <-semaphore }()
			uploaded, err := s.client.UploadPart(uploadCtx, &awss3.UploadPartInput{
				Bucket: aws.String(s.bucket), Key: aws.String(objectKey), UploadId: created.UploadId,
				PartNumber: aws.Int32(partNumber), Body: bytes.NewReader(content), ContentLength: aws.Int64(length),
			})
			if err != nil {
				errorMu.Lock()
				if uploadErr == nil {
					uploadErr = err
					cancelUploads()
				}
				errorMu.Unlock()
				return
			}
			parts[index] = types.CompletedPart{PartNumber: aws.Int32(partNumber), ETag: uploaded.ETag}
		})
	}
	if readErr == nil {
		readErr = reader.complete()
	}
	wait.Wait()
	if uploadErr != nil {
		return Object{}, classifyS3("upload object part", uploadErr)
	}
	if readErr != nil {
		return Object{}, fmt.Errorf("read multipart object: %w", readErr)
	}
	if _, err := s.client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: aws.String(s.bucket), Key: aws.String(objectKey), UploadId: created.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}, IfNoneMatch: aws.String("*"),
	}); err != nil {
		return Object{}, classifyS3("complete multipart upload", err)
	}
	metadata, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(objectKey)})
	if err != nil {
		return Object{}, classifyS3("verify multipart object", err)
	}
	if aws.ToInt64(metadata.ContentLength) != size {
		return Object{}, fmt.Errorf("verify multipart object size: %w", ErrTemporary)
	}
	var checksum [sha256.Size]byte
	copy(checksum[:], hash.Sum(nil))
	return Object{Key: key, Size: size, SHA256: checksum}, nil
}

func (s *s3Store) Open(ctx context.Context, key string, byteRange *ByteRange) (ReadObject, error) {
	if err := validateObjectKey(key); err != nil {
		return ReadObject{}, err
	}
	input := &awss3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key(key))}
	if byteRange != nil {
		if byteRange.Offset < 0 || byteRange.Length <= 0 || byteRange.Length-1 > math.MaxInt64-byteRange.Offset {
			return ReadObject{}, fmt.Errorf("invalid byte range: %w", ErrConfiguration)
		}
		input.Range = aws.String(fmt.Sprintf("bytes=%d-%d", byteRange.Offset, byteRange.Offset+byteRange.Length-1))
	}
	output, err := s.client.GetObject(ctx, input)
	if err != nil {
		return ReadObject{}, classifyS3("open object", err)
	}
	objectSize := aws.ToInt64(output.ContentLength)
	if output.ContentRange != nil {
		var start, end int64
		if _, scanErr := fmt.Sscanf(aws.ToString(output.ContentRange), "bytes %d-%d/%d", &start, &end, &objectSize); scanErr != nil {
			_ = output.Body.Close()
			return ReadObject{}, fmt.Errorf("parse storage content range: %w", ErrTemporary)
		}
	}
	return ReadObject{Body: output.Body, Size: aws.ToInt64(output.ContentLength), ObjectSize: objectSize}, nil
}

func (s *s3Store) Stat(ctx context.Context, key string) (Object, error) {
	if err := validateObjectKey(key); err != nil {
		return Object{}, err
	}
	output, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key(key))})
	if err != nil {
		return Object{}, classifyS3("stat object", err)
	}
	return Object{Key: key, Size: aws.ToInt64(output.ContentLength)}, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	if err := validateObjectKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key(key))})
	if err != nil {
		return classifyS3("delete object", err)
	}
	return nil
}

func (s *s3Store) key(key string) string { return path.Join(s.prefix, key) }

func validateObjectKey(key string) error {
	if key == "" || strings.ContainsRune(key, 0) || strings.HasPrefix(key, "/") || path.Clean(key) != key {
		return fmt.Errorf("invalid object key: %w", ErrConfiguration)
	}
	for segment := range strings.SplitSeq(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid object key: %w", ErrConfiguration)
		}
	}
	return nil
}

func resolveRange(size int64, byteRange *ByteRange) (int64, int64, error) {
	if byteRange == nil {
		return 0, size, nil
	}
	if byteRange.Offset < 0 || byteRange.Length <= 0 || byteRange.Offset >= size || byteRange.Length > size-byteRange.Offset {
		return 0, 0, fmt.Errorf("invalid byte range: %w", ErrConfiguration)
	}
	return byteRange.Offset, byteRange.Length, nil
}

func classifyLocal(operation string, err error) error {
	kind := ErrTemporary
	if errors.Is(err, fs.ErrNotExist) {
		kind = ErrMissing
	} else if errors.Is(err, fs.ErrPermission) {
		kind = ErrConfiguration
	}
	return fmt.Errorf("%s: %w: %w", operation, kind, err)
}

func classifyS3(operation string, err error) error {
	kind := ErrTemporary
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			kind = ErrMissing
		case "NoSuchBucket", "InvalidAccessKeyId", "SignatureDoesNotMatch", "AccessDenied":
			kind = ErrConfiguration
		}
	}
	if responseErr, ok := errors.AsType[*awshttp.ResponseError](err); ok {
		switch responseErr.HTTPStatusCode() {
		case http.StatusNotFound:
			kind = ErrMissing
		case http.StatusPreconditionFailed:
			kind = ErrExists
		case http.StatusUnauthorized, http.StatusForbidden:
			kind = ErrConfiguration
		}
	}
	return fmt.Errorf("%s: %w: %w", operation, kind, err)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type exactReader struct {
	reader    io.Reader
	remaining int64
}

func (r *exactReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func (r *exactReader) complete() error {
	if r.remaining != 0 {
		return fmt.Errorf("object body is short: %w", ErrConfiguration)
	}
	var extra [1]byte
	n, err := io.ReadFull(r.reader, extra[:])
	if n != 0 {
		return fmt.Errorf("object body is long: %w", ErrConfiguration)
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("read object body: %w", err)
	}
	return nil
}

type readCloser struct {
	io.Reader
	close func() error
}

func (r *readCloser) Close() error { return r.close() }
