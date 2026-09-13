package storage

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func validS3() *S3Input {
	return &S3Input{
		S3Config:    S3Config{Endpoint: " https://rustfs.example.com:9000/ ", Region: "us-east-1", Bucket: "stocat", Prefix: "/objects/", ForcePathStyle: true},
		AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: " secret ",
	}
}

func TestBuildSettings(t *testing.T) {
	stored := &s3Credentials{AccessKeyID: "stored-id", SecretAccessKey: "stored-secret"}
	for _, tc := range []struct {
		name        string
		backendType string
		local       *LocalConfig
		s3          func(*S3Input)
		stored      *s3Credentials
		wantConfig  string
		wantSecrets string
		wantError   string
	}{
		{name: "local", backendType: TypeLocal, local: &LocalConfig{Root: "/srv/stocat/../objects/"},
			wantConfig: `{"root":"/srv/objects"}`, wantSecrets: `{}`},
		{name: "local relative root", backendType: TypeLocal, local: &LocalConfig{Root: "objects"}, wantError: "body.local.root"},
		{name: "local file system root", backendType: TypeLocal, local: &LocalConfig{Root: "/srv/.."}, wantError: "body.local.root"},
		{name: "local NUL", backendType: TypeLocal, local: &LocalConfig{Root: "/srv/\x00"}, wantError: "body.local.root"},
		{name: "local without settings", backendType: TypeLocal, wantError: "body.local"},
		{name: "local with S3 settings", backendType: TypeLocal, local: &LocalConfig{Root: "/srv"}, s3: func(*S3Input) {}, wantError: "body.local"},
		{name: "s3", backendType: TypeS3, s3: func(*S3Input) {},
			wantConfig:  `{"endpoint":"https://rustfs.example.com:9000","region":"us-east-1","bucket":"stocat","prefix":"objects","force_path_style":true}`,
			wantSecrets: `{"access_key_id":"AKIAEXAMPLE","secret_access_key":"secret"}`},
		{name: "s3 AWS endpoint", backendType: TypeS3, s3: func(in *S3Input) { in.Endpoint = ""; in.Prefix = "" },
			wantConfig:  `{"endpoint":"","region":"us-east-1","bucket":"stocat","prefix":"","force_path_style":true}`,
			wantSecrets: `{"access_key_id":"AKIAEXAMPLE","secret_access_key":"secret"}`},
		{name: "s3 without settings", backendType: TypeS3, wantError: "body.s3"},
		{name: "s3 endpoint scheme", backendType: TypeS3, s3: func(in *S3Input) { in.Endpoint = "ftp://example.com" }, wantError: "body.s3.endpoint"},
		{name: "s3 endpoint path", backendType: TypeS3, s3: func(in *S3Input) { in.Endpoint = "https://example.com/bucket" }, wantError: "body.s3.endpoint"},
		{name: "s3 endpoint credentials", backendType: TypeS3, s3: func(in *S3Input) { in.Endpoint = "https://user:pass@example.com" }, wantError: "body.s3.endpoint"},
		{name: "s3 endpoint query", backendType: TypeS3, s3: func(in *S3Input) { in.Endpoint = "https://example.com?x=1" }, wantError: "body.s3.endpoint"},
		{name: "s3 region", backendType: TypeS3, s3: func(in *S3Input) { in.Region = "EU Central" }, wantError: "body.s3.region"},
		{name: "s3 bucket uppercase", backendType: TypeS3, s3: func(in *S3Input) { in.Bucket = "Stocat" }, wantError: "body.s3.bucket"},
		{name: "s3 bucket dots", backendType: TypeS3, s3: func(in *S3Input) { in.Bucket = "sto..cat" }, wantError: "body.s3.bucket"},
		{name: "s3 prefix dot-dot", backendType: TypeS3, s3: func(in *S3Input) { in.Prefix = "a/../b" }, wantError: "body.s3.prefix"},
		{name: "s3 prefix empty segment", backendType: TypeS3, s3: func(in *S3Input) { in.Prefix = "a//b" }, wantError: "body.s3.prefix"},
		{name: "s3 missing credentials", backendType: TypeS3, s3: func(in *S3Input) { in.AccessKeyID, in.SecretAccessKey = "", "" }, wantError: "body.s3"},
		{name: "s3 one credential", backendType: TypeS3, s3: func(in *S3Input) { in.SecretAccessKey = "" }, stored: stored, wantError: "body.s3"},
		{name: "s3 keeps stored credentials", backendType: TypeS3, s3: func(in *S3Input) { in.AccessKeyID, in.SecretAccessKey = "", " " }, stored: stored,
			wantConfig:  `{"endpoint":"https://rustfs.example.com:9000","region":"us-east-1","bucket":"stocat","prefix":"objects","force_path_style":true}`,
			wantSecrets: `{"access_key_id":"stored-id","secret_access_key":"stored-secret"}`},
		{name: "s3 replaces stored credentials", backendType: TypeS3, s3: func(*S3Input) {}, stored: stored,
			wantConfig:  `{"endpoint":"https://rustfs.example.com:9000","region":"us-east-1","bucket":"stocat","prefix":"objects","force_path_style":true}`,
			wantSecrets: `{"access_key_id":"AKIAEXAMPLE","secret_access_key":"secret"}`},
		{name: "unknown type", backendType: "ftp", wantError: "body.type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s3 *S3Input
			if tc.s3 != nil {
				s3 = validS3()
				tc.s3(s3)
			}
			got, err := buildSettings(tc.backendType, tc.local, s3, tc.stored)
			if tc.wantError != "" {
				requireValidationError(t, err, tc.wantError)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			config, secrets, err := got.columns()
			if err != nil {
				t.Fatal(err)
			}
			if string(config) != tc.wantConfig || string(secrets) != tc.wantSecrets {
				t.Fatalf("settings = %s %s, want %s %s", config, secrets, tc.wantConfig, tc.wantSecrets)
			}
		})
	}
}

func requireValidationError(t *testing.T, err error, location string) {
	t.Helper()
	statusErr, ok := errors.AsType[huma.StatusError](err)
	if !ok || statusErr.GetStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("error = %v, want status 422", err)
	}
	body, _ := json.Marshal(statusErr)
	var problem struct {
		Errors []huma.ErrorDetail `json:"errors"`
	}
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatal(err)
	}
	if len(problem.Errors) != 1 || problem.Errors[0].Location != location {
		t.Fatalf("error details = %+v, want location %s", problem.Errors, location)
	}
}
