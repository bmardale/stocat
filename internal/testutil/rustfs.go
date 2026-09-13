package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// S3 contains the connection settings of an S3-compatible test server.
type S3 struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
}

// NewRustFS starts a RustFS server and registers cleanup. The server has no buckets.
// Tests skip in short mode and require Docker otherwise.
func NewRustFS(t testing.TB) S3 {
	t.Helper()
	if testing.Short() {
		t.Skip("RustFS integration tests require Docker; omit -short to run them")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	server := S3{AccessKeyID: "stocat-test", SecretAccessKey: "stocat-test-secret"}
	container, err := testcontainers.Run(ctx, "rustfs/rustfs:1.0.0-rc.6",
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithEnv(map[string]string{
			"RUSTFS_ACCESS_KEY": server.AccessKeyID,
			"RUSTFS_SECRET_KEY": server.SecretAccessKey,
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/health").WithPort("9000/tcp").WithStartupTimeout(time.Minute)),
	)
	if container != nil {
		testcontainers.CleanupContainer(t, container)
	}
	if err != nil {
		t.Fatalf("start RustFS container: %v", err)
	}
	if server.Endpoint, err = container.PortEndpoint(ctx, "9000/tcp", "http"); err != nil {
		t.Fatalf("get RustFS address: %v", err)
	}
	// The /health endpoint answers before the S3 API accepts requests.
	waitForS3(t, ctx, server)
	return server
}

// waitForS3 blocks until the server accepts signed S3 requests. Without this
// wait, the first bucket operation can fail with status 503.
func waitForS3(t testing.TB, ctx context.Context, server S3) {
	t.Helper()
	client := awss3.New(awss3.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider(server.AccessKeyID, server.SecretAccessKey, ""),
		UsePathStyle: true,
		BaseEndpoint: aws.String(server.Endpoint),
	})
	var lastErr error
	for {
		if _, lastErr = client.ListBuckets(ctx, &awss3.ListBucketsInput{}); lastErr == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("RustFS did not accept S3 requests: %v", lastErr)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
