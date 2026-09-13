package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// newS3Client ignores the AWS environment variables and shared files of the server.
// It uses only the backend settings.
func newS3Client(config S3Config, keys s3Credentials) *awss3.Client {
	options := awss3.Options{
		Region:           config.Region,
		Credentials:      credentials.NewStaticCredentialsProvider(keys.AccessKeyID, keys.SecretAccessKey, ""),
		UsePathStyle:     config.ForcePathStyle,
		RetryMaxAttempts: 1,
		// Many S3-compatible services reject the checksums that the SDK adds by default.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if config.Endpoint != "" {
		options.BaseEndpoint = aws.String(config.Endpoint)
	}
	return awss3.New(options)
}

// checkS3 writes an object first, because error responses to a write contain an error code.
// Responses to HEAD requests have no body.
func checkS3(ctx context.Context, config S3Config, keys s3Credentials) error {
	client := newS3Client(config, keys)
	bucket := aws.String(config.Bucket)
	key := aws.String(path.Join(config.Prefix, ".stocat-check-"+rand.Text()))
	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: bucket, Key: key,
		Body: bytes.NewReader(checkPayload), ContentLength: aws.Int64(int64(len(checkPayload))),
	})
	if err != nil {
		return s3Failure(err, "The credentials do not allow writing objects to the bucket.")
	}
	readErr := readS3Object(ctx, client, bucket, key)
	_, deleteErr := client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: bucket, Key: key})
	if readErr != nil {
		return readErr
	}
	if deleteErr != nil {
		return s3Failure(deleteErr, "The credentials do not allow deleting objects in the bucket.")
	}
	return nil
}

func readS3Object(ctx context.Context, client *awss3.Client, bucket, key *string) error {
	output, err := client.GetObject(ctx, &awss3.GetObjectInput{Bucket: bucket, Key: key})
	if err != nil {
		return s3Failure(err, "The credentials do not allow reading objects in the bucket.")
	}
	defer func() { _ = output.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(output.Body, int64(len(checkPayload))+1))
	if err != nil {
		return s3Failure(err, "The server cannot read objects in the bucket.")
	}
	if !bytes.Equal(content, checkPayload) {
		return failure("The bucket returned different content than the server wrote.", nil)
	}
	return nil
}

// s3Failure converts an SDK error to a message for the administrator. The
// forbidden message describes the operation that the service rejected.
func s3Failure(err error, forbidden string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return failure("The storage service did not answer in time.", err)
	}
	// The SDK wraps connection errors in a response error with status 0.
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return failure("The server does not trust the TLS certificate of the endpoint.", err)
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return failure("The server cannot connect to the endpoint.", err)
	}
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		switch apiErr.ErrorCode() {
		case "NoSuchBucket":
			return failure("The bucket does not exist.", err)
		case "InvalidAccessKeyId":
			return failure("The access key ID is not valid.", err)
		case "SignatureDoesNotMatch":
			return failure("The secret access key is not correct.", err)
		case "AuthorizationHeaderMalformed", "PermanentRedirect", "IllegalLocationConstraintException":
			return failure("The bucket is in a different region.", err)
		case "AccessDenied":
			return failure(forbidden, err)
		}
	}
	if responseErr, ok := errors.AsType[*awshttp.ResponseError](err); ok && responseErr.HTTPStatusCode() != 0 {
		switch status := responseErr.HTTPStatusCode(); status {
		case http.StatusNotFound:
			return failure("The bucket does not exist.", err)
		case http.StatusForbidden:
			return failure(forbidden, err)
		case http.StatusMovedPermanently:
			return failure("The bucket is in a different region.", err)
		default:
			return failure(fmt.Sprintf("The storage service returned status %d.", status), err)
		}
	}
	return failure("The server cannot use the storage service.", err)
}
