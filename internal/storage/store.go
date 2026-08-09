package storage

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectStore is backed by isolated S3-compatible storage in production.
type ObjectStore interface {
	Put(key string, data []byte) (string, error)
	Get(key string) ([]byte, error)
}
type MemoryStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{objects: map[string][]byte{}} }
func (s *MemoryStore) Put(key string, data []byte) (string, error) {
	if key == "" || len(data) == 0 {
		return "", errors.New("object key and data are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), data...)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}
func (s *MemoryStore) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, errors.New("object not found")
	}
	return append([]byte(nil), data...), nil
}

// HTTPStore writes only to a dedicated object-storage gateway. The gateway is
// responsible for physical storage, retention, and access isolation.
type HTTPStore struct {
	baseURL, token string
	client         *http.Client
}
type S3Store struct {
	client *awss3.Client
	bucket string
}

func NewS3Store(endpoint, region, bucket, accessKey, secretKey string) (*S3Store, error) {
	if endpoint == "" || region == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("S3 endpoint, region, bucket and credentials are required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	if err != nil {
		return nil, err
	}
	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.BaseEndpoint = &endpoint; o.UsePathStyle = true })
	return &S3Store{client: client, bucket: bucket}, nil
}
func (s *S3Store) Put(key string, data []byte) (string, error) {
	if key == "" || len(data) == 0 {
		return "", errors.New("object key and data are required")
	}
	_, err := s.client.PutObject(context.Background(), &awss3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: io.NopCloser(&byteReader{data: data})})
	if err != nil {
		return "", fmt.Errorf("S3 put object: %w", err)
	}
	return digest(data), nil
}
func (s *S3Store) Get(key string) ([]byte, error) {
	out, err := s.client.GetObject(context.Background(), &awss3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, fmt.Errorf("S3 get object: %w", err)
	}
	defer out.Body.Close()
	return io.ReadAll(io.LimitReader(out.Body, 50<<20))
}

func NewHTTPStore(baseURL, token string, client *http.Client) (*HTTPStore, error) {
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("invalid object storage URL: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPStore{baseURL: baseURL, token: token, client: client}, nil
}
func NewMTLSClient(certFile, keyFile, caFile string) (*http.Client, error) {
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load storage mTLS certificate: %w", err)
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	if caFile != "" {
		caData, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read storage CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caData) {
			return nil, errors.New("storage CA contains no certificate")
		}
		config.RootCAs = pool
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: config}}, nil
}
func (s *HTTPStore) Put(key string, data []byte) (string, error) {
	if key == "" || len(data) == 0 {
		return "", errors.New("object key and data are required")
	}
	u := s.baseURL + "/" + url.PathEscape(key)
	req, err := http.NewRequest(http.MethodPut, u, io.NopCloser(&byteReader{data: data}))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("object storage request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("object storage returned %s", resp.Status)
	}
	return digest(data), nil
}
func (s *HTTPStore) Get(key string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/"+url.PathEscape(key), nil)
	if err != nil {
		return nil, err
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("object storage request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("object storage returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 50<<20))
}

type byteReader struct {
	data   []byte
	offset int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}
func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
