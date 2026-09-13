// Package storage manages the storage backends that hold file contents.
package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/bmardale/stocat/internal/db"
	"github.com/danielgtaylor/huma/v2"
)

const (
	TypeLocal = "local"
	TypeS3    = "s3"
)

type Backend struct {
	ID      string       `json:"id" example:"stb_01K4W9T5V8QK3M7ZB0YHXC2FNE"`
	Name    string       `json:"name"`
	Type    string       `json:"type" enum:"local,s3"`
	Enabled bool         `json:"enabled"`
	Local   *LocalConfig `json:"local,omitempty" doc:"Present only for local backends."`
	S3      *S3Config    `json:"s3,omitempty" doc:"Present only for S3 backends. It never contains credentials."`
}

type LocalConfig struct {
	Root string `json:"root" minLength:"1" maxLength:"4096" doc:"Absolute path of the directory that stores objects." example:"/var/lib/stocat/objects"`
}

type S3Config struct {
	Endpoint       string `json:"endpoint" maxLength:"2048" doc:"Base URL of an S3-compatible service. Use an empty value for AWS." example:"https://s3.eu-central-1.amazonaws.com"`
	Region         string `json:"region" minLength:"1" maxLength:"64" example:"eu-central-1"`
	Bucket         string `json:"bucket" minLength:"3" maxLength:"63" example:"stocat"`
	Prefix         string `json:"prefix" maxLength:"512" doc:"Key prefix of all objects. Use an empty value for the bucket root." example:"objects"`
	ForcePathStyle bool   `json:"force_path_style" doc:"Put the bucket name in the URL path instead of the host name. RustFS needs this."`
}

type S3Input struct {
	S3Config
	AccessKeyID     string `json:"access_key_id,omitempty" maxLength:"256" writeOnly:"true" doc:"Required when you create a backend. On update, leave both credentials empty to keep the stored credentials."`
	SecretAccessKey string `json:"secret_access_key,omitempty" maxLength:"256" writeOnly:"true"`
}

// s3Credentials is the plaintext of encrypted_secrets for S3 backends.
type s3Credentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// settings contains validated settings. Exactly one of local and s3 is set.
type settings struct {
	local       *LocalConfig
	s3          *S3Config
	credentials s3Credentials
}

// columns returns the values of the config column and the plaintext of the encrypted_secrets column.
func (s settings) columns() (config, secrets []byte, err error) {
	var configValue, secretsValue any = s.local, struct{}{}
	if s.s3 != nil {
		configValue, secretsValue = s.s3, s.credentials
	}
	if config, err = json.Marshal(configValue); err != nil {
		return nil, nil, fmt.Errorf("marshal config: %w", err)
	}
	if secrets, err = json.Marshal(secretsValue); err != nil {
		return nil, nil, fmt.Errorf("marshal secrets: %w", err)
	}
	return config, secrets, nil
}

var (
	bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	regionPattern = regexp.MustCompile(`^[a-z0-9-]+$`)
)

func invalid(location, message string) error {
	return huma.Error422UnprocessableEntity(message, &huma.ErrorDetail{Location: location, Message: message})
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalid("body.name", "Enter a name.")
	}
	return name, nil
}

// buildSettings validates the settings of a backend type. The stored
// credentials replace empty S3 credentials. Use nil when you create a backend.
func buildSettings(backendType string, local *LocalConfig, s3 *S3Input, stored *s3Credentials) (settings, error) {
	switch backendType {
	case TypeLocal:
		if local == nil || s3 != nil {
			return settings{}, invalid("body.local", "Send only the local settings for a local backend.")
		}
		config, err := validateLocal(*local)
		if err != nil {
			return settings{}, err
		}
		return settings{local: &config}, nil
	case TypeS3:
		if s3 == nil || local != nil {
			return settings{}, invalid("body.s3", "Send only the S3 settings for an S3 backend.")
		}
		config, err := validateS3(s3.S3Config)
		if err != nil {
			return settings{}, err
		}
		credentials, err := validateCredentials(*s3, stored)
		if err != nil {
			return settings{}, err
		}
		return settings{s3: &config, credentials: credentials}, nil
	default:
		return settings{}, invalid("body.type", "Use a supported backend type.")
	}
}

func validateLocal(config LocalConfig) (LocalConfig, error) {
	if strings.ContainsRune(config.Root, 0) || !filepath.IsAbs(config.Root) {
		return LocalConfig{}, invalid("body.local.root", "Enter an absolute directory path.")
	}
	root := filepath.Clean(config.Root)
	if filepath.Dir(root) == root {
		return LocalConfig{}, invalid("body.local.root", "Use a directory below the file system root.")
	}
	return LocalConfig{Root: root}, nil
}

func validateS3(config S3Config) (S3Config, error) {
	endpoint, err := normalizeEndpoint(strings.TrimSpace(config.Endpoint))
	if err != nil {
		return S3Config{}, err
	}
	region := strings.TrimSpace(config.Region)
	if !regionPattern.MatchString(region) {
		return S3Config{}, invalid("body.s3.region", "Enter a region with lowercase letters, digits, and hyphens.")
	}
	bucket := strings.TrimSpace(config.Bucket)
	if !bucketPattern.MatchString(bucket) || strings.Contains(bucket, "..") {
		return S3Config{}, invalid("body.s3.bucket", "Enter a bucket name with 3 to 63 lowercase letters, digits, dots, and hyphens.")
	}
	prefix := strings.Trim(strings.TrimSpace(config.Prefix), "/")
	if prefix != "" && (strings.ContainsRune(prefix, 0) || slices.ContainsFunc(strings.Split(prefix, "/"), func(segment string) bool {
		return segment == "" || segment == "." || segment == ".."
	})) {
		return S3Config{}, invalid("body.s3.prefix", "Enter a prefix without empty, dot, or dot-dot segments.")
	}
	return S3Config{Endpoint: endpoint, Region: region, Bucket: bucket, Prefix: prefix, ForcePathStyle: config.ForcePathStyle}, nil
}

func normalizeEndpoint(endpoint string) (string, error) {
	if endpoint == "" {
		return "", nil
	}
	const message = "Enter an HTTP or HTTPS URL without a path, query, or credentials."
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", invalid("body.s3.endpoint", message)
	}
	return u.Scheme + "://" + u.Host, nil
}

func validateCredentials(input S3Input, stored *s3Credentials) (s3Credentials, error) {
	credentials := s3Credentials{
		AccessKeyID:     strings.TrimSpace(input.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(input.SecretAccessKey),
	}
	if credentials.AccessKeyID == "" && credentials.SecretAccessKey == "" && stored != nil {
		return *stored, nil
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return s3Credentials{}, invalid("body.s3", "Enter the access key ID and the secret access key.")
	}
	return credentials, nil
}

func backendFromRow(row db.StorageBackend) (Backend, error) {
	backend := Backend{ID: row.PublicID, Name: row.Name, Type: row.Type, Enabled: row.Enabled}
	var err error
	switch row.Type {
	case TypeLocal:
		backend.Local = &LocalConfig{}
		err = json.Unmarshal(row.Config, backend.Local)
	case TypeS3:
		backend.S3 = &S3Config{}
		err = json.Unmarshal(row.Config, backend.S3)
	default:
		err = fmt.Errorf("unknown backend type %q", row.Type)
	}
	if err != nil {
		return Backend{}, fmt.Errorf("decode storage backend %s: %w", row.PublicID, err)
	}
	return backend, nil
}
