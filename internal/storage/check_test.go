package storage

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bmardale/stocat/internal/testutil"
)

func checkResult(err error) string {
	if err == nil {
		return ""
	}
	return failureMessage(err)
}

func TestCheckLocal(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	readOnly := filepath.Join(root, "read-only")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })

	for _, tc := range []struct {
		name, root, want string
	}{
		{"writable", root, ""},
		{"missing", filepath.Join(root, "missing"), "The directory does not exist."},
		{"file", file, "The path is not a directory."},
		{"read-only", readOnly, "The server cannot write to the directory."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "read-only" && os.Geteuid() == 0 {
				t.Skip("root can write to read-only directories")
			}
			if got := checkResult(checkLocal(tc.root)); got != tc.want {
				t.Fatalf("checkLocal() = %q, want %q", got, tc.want)
			}
		})
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stocat-check-") {
			t.Fatalf("check left %s in the directory", entry.Name())
		}
	}
}

func newTestBucket(t *testing.T, server testutil.S3, bucket string) (S3Config, s3Credentials) {
	t.Helper()
	config := S3Config{Endpoint: server.Endpoint, Region: "us-east-1", Bucket: bucket, ForcePathStyle: true}
	keys := s3Credentials{AccessKeyID: server.AccessKeyID, SecretAccessKey: server.SecretAccessKey}
	if _, err := newS3Client(config, keys).CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return config, keys
}

func closedEndpoint(t *testing.T) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + address
}

func TestCheckS3(t *testing.T) {
	server := testutil.NewRustFS(t)
	config, keys := newTestBucket(t, server, "stocat")
	closed := closedEndpoint(t)

	for _, tc := range []struct {
		name   string
		change func(*S3Config, *s3Credentials)
		want   string
	}{
		{"valid", func(*S3Config, *s3Credentials) {}, ""},
		{"prefix", func(c *S3Config, _ *s3Credentials) { c.Prefix = "objects/checks" }, ""},
		{"missing bucket", func(c *S3Config, _ *s3Credentials) { c.Bucket = "missing" }, "The bucket does not exist."},
		{"unknown access key", func(_ *S3Config, k *s3Credentials) { k.AccessKeyID = "unknown" }, "The access key ID is not valid."},
		{"wrong secret", func(_ *S3Config, k *s3Credentials) { k.SecretAccessKey = "wrong-secret" }, "The secret access key is not correct."},
		{"closed port", func(c *S3Config, _ *s3Credentials) { c.Endpoint = closed }, "The server cannot connect to the endpoint."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, keys := config, keys
			tc.change(&config, &keys)
			ctx, cancel := context.WithTimeout(t.Context(), checkTimeout)
			defer cancel()
			if got := checkResult(checkS3(ctx, config, keys)); got != tc.want {
				t.Fatalf("checkS3() = %q, want %q", got, tc.want)
			}
		})
	}

	objects, err := newS3Client(config, keys).ListObjectsV2(t.Context(), &awss3.ListObjectsV2Input{Bucket: aws.String("stocat")})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects.Contents) != 0 {
		t.Fatalf("checks left %d objects in the bucket", len(objects.Contents))
	}
}
