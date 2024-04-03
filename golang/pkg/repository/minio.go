package repository

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"
	"gotest.tools/gotestsum/log"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/replicate/keepsake/golang/pkg/concurrency"
	"github.com/replicate/keepsake/golang/pkg/console"
	"github.com/replicate/keepsake/golang/pkg/errors"
	"github.com/replicate/keepsake/golang/pkg/files"
)

type minioConfig struct {
	URL               string `mapstructure:"url" json:"url"`                                 // storage 的符合 URL 一行配置
	Provider          string `mapstructure:"provider" json:"provider"`                       // 服务类型
	DefaultBucketName string `mapstructure:"default_bucket_name" json:"default_bucket_name"` // 默认 bucket
	Region            string `mapstructure:"region" json:"region"`                           // region for aws
	AkId              string `mapstructure:"ak_id" json:"ak_id"`                             // api key
	AkSecret          string `mapstructure:"ak_secret" json:"ak_secret"`                     // api secret
	Endpoint          string `mapstructure:"endpoint" json:"endpoint"`                       // api endpoint,
	MaxRetries        int    `mapstructure:"max_retries" json:"max_retries"`
	NoSSL             bool   `mapstructure:"no_ssl" json:"no_ssl"`
	NoPermCheck       bool   `mapstructure:"no_perm_check" json:"no_perm_check"`
	ExternalHost      string `mapstructure:"external_host" json:"external_host"` // 接受 "http://host", 或者 "host"
	ExternalSSL       bool   `mapstructure:"external_ssl" json:"external_ssl"`   //
	DataDir           string `mapstructure:"data_dir" json:"data_dir,omitempty"`
}

func (c *minioConfig) ParseURL(rawUrl string) error {
	info, err := url.Parse(rawUrl)
	if err != nil {
		return err
	}

	if len(info.Scheme) > 0 {
		c.Provider = info.Scheme
	}

	if info.User != nil {
		if len(info.User.Username()) > 0 {
			c.AkId = info.User.Username()
		}
		if pass, ok := info.User.Password(); ok && len(pass) > 0 {
			c.AkSecret = pass
		}
	}

	if len(info.Host) > 0 {
		c.Endpoint = info.Host
	} else {
		return fmt.Errorf("Not contains host")
	}

	query := info.Query()

	if bucket := query.Get("bucket"); len(bucket) > 0 {
		c.DefaultBucketName = bucket
	}

	if maxRetries := query.Get("max-retries"); len(maxRetries) > 0 {
		if i, e := strconv.Atoi(maxRetries); e == nil {
			c.MaxRetries = i
		}
	}
	// obs如果不设置region或者为空会报不兼容的错误, 这里给一个默认region
	if region := query.Get("region"); len(region) > 0 {
		c.Region = region
	}

	if noSSL := query.Get("no-ssl"); len(noSSL) > 0 {
		if len(noSSL) > 0 {
			noSSL = strings.ToLower(noSSL)
			if noSSL == "1" || noSSL == "yes" || noSSL == "true" {
				c.NoSSL = true
			}
		}
	}

	if externalHost := query.Get("external-host"); len(externalHost) > 0 {
		c.ExternalHost = externalHost
	}

	//
	if externalSSL := query.Get("external-ssl"); len(externalSSL) > 0 {
		if len(externalSSL) > 0 {
			externalSSL = strings.ToLower(externalSSL)
			if externalSSL == "1" || externalSSL == "yes" || externalSSL == "true" {
				c.ExternalSSL = true
			}
		}
	}

	if len(info.Path) > 0 {
		c.DataDir = path.Join("/", info.Path)
	}

	return nil
}

func parseURL(rawUrl string) (*minioConfig, error) {
	c := &minioConfig{}
	st := c.ParseURL(rawUrl)
	return c, st
}

type MinioRepository struct {
	bucketName string
	root       string
	cfg        *minioConfig
	client     *minio.Client
}

func NewMinioRepository(bucket, root string) (*MinioRepository, error) {
	cfg, err := parseURL(bucket)
	if err != nil {
		return nil, err
	}

	// s.sess, err = session.NewSession(&aws.Config{
	// 	Region:                        aws.String(region),
	// 	CredentialsChainVerboseErrors: aws.Bool(true),
	// })
	// if err != nil {
	// 	return nil, errors.RepositoryConfigurationError(fmt.Sprintf("Failed to connect to S3: %s", err))
	// }
	// s.svc = s3.New(s.sess)
	endpoint := cfg.Endpoint

	log.Debugf("endpoint=%v, bucket=%v", endpoint, cfg.DefaultBucketName)

	dialOptions := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AkId, cfg.AkSecret, ""),
		Secure: !cfg.NoSSL,
	}

	cli, err := minio.New(endpoint, dialOptions)
	if err != nil {
		return nil, err
	}
	s := &MinioRepository{
		bucketName: bucket,
		root:       root,
		client:     cli,
		cfg:        cfg,
	}

	return s, nil
}

func (s *MinioRepository) RootURL() string {
	return fmt.Sprintf("minio://%s/%s", s.bucketName, s.root)
	// assignedURL, err := s.client.PresignedGetObject(context.TODO(), s.bucketName, s.root, 10*time.Hour, url.Values{})
	// if err != nil {
	// 	return ""
	// }
	// return assignedURL.String()
}

// Get data at path
func (s *MinioRepository) Get(path string) ([]byte, error) {
	key := filepath.Join(s.root, path)
	// obj, err := s.svc.GetObject(&s3.GetObjectInput{
	// 	Bucket: aws.String(s.bucketName),
	// 	Key:    aws.String(key),
	// })
	obj, err := s.client.GetObject(context.TODO(), s.bucketName, key, minio.GetObjectOptions{})

	if err != nil {
		return nil, errors.ReadError(fmt.Sprintf("Failed to read %s/%s: %s", s.RootURL(), path, err))
	}
	body, err := io.ReadAll(obj)
	if err != nil {
		return nil, errors.ReadError(fmt.Sprintf("Failed to read body from %s/%s: %s", s.RootURL(), path, err))
	}
	return body, nil
}

func (s *MinioRepository) Delete(path string) error {
	console.Debug("Deleting %s/%s...", s.RootURL(), path)
	key := filepath.Join(s.root, path)
	candidates := s.client.ListObjects(context.TODO(), s.bucketName, minio.ListObjectsOptions{
		Prefix: key,
	})
	errorCh := s.client.RemoveObjects(context.TODO(), s.bucketName, candidates, minio.RemoveObjectsOptions{})
	for err := range errorCh {
		return errors.WriteError(fmt.Sprintf("Failed to delete %s/%s: %v", s.RootURL(), path, err.Err))
	}

	// iter := s3manager.NewDeleteListIterator(s.svc, &s3.ListObjectsInput{
	// 	Bucket: &s.bucketName,
	// 	Prefix: &key,
	// })
	// if err := s3manager.NewBatchDeleteWithClient(s.svc).Delete(aws.BackgroundContext(), iter); err != nil {
	// 	return errors.WriteError(fmt.Sprintf("Failed to delete %s/%s: %v", s.RootURL(), path, err))
	// }
	return nil
}

// Put data at path
func (s *MinioRepository) Put(path string, data []byte) error {
	key := filepath.Join(s.root, path)
	_, err := s.client.PutObject(context.TODO(), s.bucketName, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{})
	// uploader := s3manager.NewUploader(s.sess)
	// _, err := uploader.Upload(&s3manager.UploadInput{
	// 	Bucket: aws.String(s.bucketName),
	// 	Key:    aws.String(key),
	// 	Body:   bytes.NewReader(data),
	// })
	if err != nil {
		return errors.WriteError(fmt.Sprintf("Unable to upload to %s/%s: %v", s.RootURL(), path, err))
	}
	return nil
}

func (s *MinioRepository) PutPath(localPath string, destPath string) error {
	files, err := getListOfFilesToPut(localPath, filepath.Join(s.root, destPath))
	if err != nil {
		return errors.WriteError(err.Error())
	}
	queue := concurrency.NewWorkerQueue(context.Background(), maxWorkers)

	for _, file := range files {
		// Variables used in closure
		file := file
		err := queue.Go(func() error {
			data, err := os.ReadFile(file.Source)
			if err != nil {
				return err
			}
			_, err = s.client.PutObject(context.TODO(), s.bucketName, file.Dest, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{})
			// uploader := s3manager.NewUploader(s.sess)
			// _, err = uploader.Upload(&s3manager.UploadInput{
			// 	Bucket: aws.String(s.bucketName),
			// 	Key:    aws.String(file.Dest),
			// 	Body:   bytes.NewReader(data),
			// })
			return err
		})
		if err != nil {
			return errors.WriteError(err.Error())
		}
	}

	if err := queue.Wait(); err != nil {
		return errors.WriteError(err.Error())
	}
	return nil
}

func (s *MinioRepository) PutPathTar(localPath, tarPath, includePath string) error {
	if !strings.HasSuffix(tarPath, ".tar.gz") {
		return fmt.Errorf("PutPathTar: tarPath must end with .tar.gz")
	}

	reader, writer := io.Pipe()

	// TODO: This doesn't cancel elegantly on error -- we should use the context returned here and check if it is done.
	errs, _ := errgroup.WithContext(context.TODO())

	errs.Go(func() error {
		if err := putPathTar(localPath, writer, filepath.Base(tarPath), includePath); err != nil {
			return err
		}
		return writer.Close()
	})
	errs.Go(func() error {
		key := filepath.Join(s.root, tarPath)
		_, err := s.client.PutObject(context.TODO(), s.bucketName, key, reader, -1, minio.PutObjectOptions{})

		// uploader := s3manager.NewUploader(s.sess)
		// _, err := uploader.Upload(&s3manager.UploadInput{
		// 	Bucket: aws.String(s.bucketName),
		// 	Key:    aws.String(key),
		// 	Body:   reader,
		// })
		return err
	})
	if err := errs.Wait(); err != nil {
		return errors.WriteError(err.Error())
	}
	return nil
}

// GetPath recursively copies repoDir to localDir
func (s *MinioRepository) GetPath(remoteDir string, localDir string) error {
	prefix := filepath.Join(s.root, remoteDir)
	// iter := new(s3manager.DownloadObjectsIterator)
	files := []*os.File{}
	defer func() {
		for _, f := range files {
			if err := f.Close(); err != nil {
				console.Warn("Failed to close file %s", f.Name())
			}
		}
	}()

	keys := []string{}
	// err := s.svc.ListObjectsV2PagesWithContext(aws.BackgroundContext(), &s3.ListObjectsV2Input{
	// 	Bucket: aws.String(s.bucketName),
	// 	Prefix: aws.String(prefix),
	// }, func(output *s3.ListObjectsV2Output, last bool) bool {
	// 	for _, object := range output.Contents {
	// 		keys = append(keys, object.Key)
	// 	}
	// 	return true
	// })
	opts := minio.ListObjectsOptions{
		UseV1:   true,
		Prefix:  prefix,
		MaxKeys: int(1000),
	}
	resultCh := s.client.ListObjects(context.TODO(), s.bucketName, opts)
	for r := range resultCh {
		if r.Err != nil {
			return errors.ReadError(fmt.Sprintf("Failed to list objects in s3://%s/%s: %v", s.bucketName, prefix, r.Err))
		}

		key := r.Key
		// if s.root != "" {
		// 	key = strings.TrimPrefix(strings.TrimPrefix(key, s.root), "/")
		// }
		keys = append(keys, key)
	}

	for _, key := range keys {
		relPath, err := filepath.Rel(prefix, key)
		if err != nil {
			return fmt.Errorf("Failed to determine directory of %s relative to %s: %v", key, prefix, err)
		}
		localPath := filepath.Join(localDir, relPath)
		localDir := filepath.Dir(localPath)
		if err := os.MkdirAll(localDir, 0755); err != nil {
			return fmt.Errorf("Failed to create directory %s: %v", localDir, err)
		}

		f, err := os.Create(localPath)
		if err != nil {
			return fmt.Errorf("Failed to create file %s: %v", localPath, err)
		}

		console.Debug("Downloading %s to %s", key, localPath)
		reader, err := s.client.GetObject(context.TODO(), s.bucketName, key, minio.GetObjectOptions{})
		if err != nil {
			return fmt.Errorf("Failed to download directory of %s relative to %s: %v", key, prefix, err)
		}
		_, err = io.Copy(f, reader)
		if err != nil {
			return fmt.Errorf("Failed to download directory of %s relative to %s: %v", key, prefix, err)
		}
		// iter.Objects = append(iter.Objects, s3manager.BatchDownloadObject{
		// 	Object: &s3.GetObjectInput{
		// 		Bucket: aws.String(s.bucketName),
		// 		Key:    key,
		// 	},
		// 	Writer: f,
		// })
		files = append(files, f)
	}

	// downloader := s3manager.NewDownloader(s.sess)
	// if err := downloader.DownloadWithIterator(aws.BackgroundContext(), iter); err != nil {
	// 	return errors.ReadError(fmt.Sprintf("Failed to download s3://%s/%s to %s", s.bucketName, prefix, localDir))
	// }
	return nil
}

func (s *MinioRepository) GetPathTar(tarPath, localPath string) error {
	// archiver doesn't let us use readers, so download to temporary file
	// TODO: make a better tar implementation
	tmpdir, err := files.TempDir("tar")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpdir)
	tmptarball := filepath.Join(tmpdir, filepath.Base(tarPath))
	if err := s.GetPath(tarPath, tmptarball); err != nil {
		return err
	}
	exists, err := files.FileExists(tmptarball)
	if err != nil {
		return err
	}
	if !exists {
		return errors.DoesNotExist(fmt.Sprintf("GetPathTar: does not exist: %v", tmptarball))
	}
	return extractTar(tmptarball, localPath)
}

func (s *MinioRepository) GetPathItemTar(tarPath, itemPath, localPath string) error {
	// archiver doesn't let us use readers, so download to temporary file
	// TODO: make a better tar implementation
	tmpdir, err := files.TempDir("tar")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpdir)
	tmptarball := filepath.Join(tmpdir, filepath.Base(tarPath))
	if err := s.GetPath(tarPath, tmptarball); err != nil {
		return err
	}
	exists, err := files.FileExists(tmptarball)
	if err != nil {
		return err
	}
	if !exists {
		return errors.DoesNotExist("Path does not exist: " + tmptarball)
	}
	return extractTarItem(tmptarball, itemPath, localPath)
}

func (s *MinioRepository) ListRecursive(results chan<- ListResult, dir string) {
	s.listRecursive(results, dir, func(_ string) bool { return true })
}

func (s *MinioRepository) MatchFilenamesRecursive(results chan<- ListResult, folder string, filename string) {
	s.listRecursive(results, folder, func(key string) bool {
		return filepath.Base(key) == filename
	})
}

// List files in a path non-recursively
func (s *MinioRepository) List(dir string) ([]string, error) {
	results := []string{}
	prefix := filepath.Join(s.root, dir)

	// prefixes must end with / and must not end with /
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	prefix = strings.TrimPrefix(prefix, "/")

	opts := minio.ListObjectsOptions{
		UseV1:   true,
		Prefix:  prefix,
		MaxKeys: int(1000),
	}
	resultCh := s.client.ListObjects(context.TODO(), s.bucketName, opts)
	for r := range resultCh {
		if r.Err != nil {
			// results <- ListResult{Error: fmt.Errorf("Failed to list objects in s3://%s: %s", s.bucketName, err)}
			continue
		}

		key := r.Key
		if s.root != "" {
			key = strings.TrimPrefix(strings.TrimPrefix(key, s.root), "/")
		}
		results = append(results, key)
	}
	return results, nil
	// if filter(key) {
	// 	// If S3 gives us an empty/bad etag, then make it blank and cause sync instead of throwing error
	// 	// Also, the etag includes quotes for some reason
	// 	md5, _ := hex.DecodeString(strings.Replace(r.ETag, "\"", "", -1))
	// 	results <- ListResult{Path: key, MD5: md5}
	// }
	// err := s.svc.ListObjectsPages(&s3.ListObjectsInput{
	// 	Bucket:    aws.String(s.bucketName),
	// 	Prefix:    aws.String(prefix),
	// 	Delimiter: aws.String("/"),
	// 	MaxKeys:   aws.Int64(1000),
	// }, func(page *s3.ListObjectsOutput, lastPage bool) bool {
	// 	for _, value := range page.Contents {
	// 		key := *value.Key
	// 		if s.root != "" {
	// 			key = strings.TrimPrefix(strings.TrimPrefix(key, s.root), "/")
	// 		}
	// 		results = append(results, key)
	// 	}
	// 	return true
	// })
	// if err != nil {
	// 	return nil, errors.ReadError(err.Error())
	// }
	// return results, nil
}

func (s *MinioRepository) ListTarFile(tarPath string) ([]string, error) {
	// archiver doesn't let us use readers, so download to temporary file
	// TODO: make a better tar implementation
	tmpdir, err := files.TempDir("tar")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpdir)
	tmptarball := filepath.Join(tmpdir, filepath.Base(tarPath))
	if err := s.GetPath(tarPath, tmptarball); err != nil {
		return nil, err
	}
	exists, err := files.FileExists(tmptarball)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.DoesNotExist("Path does not exist: " + tmptarball)
	}

	files, err := getListOfFilesInTar(tmptarball)
	if err != nil {
		return nil, err
	}

	tarname := filepath.Base(strings.TrimSuffix(tarPath, ".tar.gz"))
	for idx := range files {
		files[idx] = strings.TrimPrefix(files[idx], tarname+"/")
	}

	return files, nil
}

// func CreateS3Bucket(region, bucket string) (err error) {
// 	sess, err := session.NewSession(&aws.Config{
// 		Region:                        aws.String(region),
// 		CredentialsChainVerboseErrors: aws.Bool(true),
// 	})
// 	if err != nil {
// 		return fmt.Errorf("Failed to connect to S3: %w", err)
// 	}
// 	svc := s3.New(sess)

// 	_, err = svc.CreateBucket(&s3.CreateBucketInput{
// 		Bucket: aws.String(bucket),
// 	})
// 	if err != nil {
// 		return errors.WriteError(fmt.Sprintf("Unable to create bucket %q, %v", bucket, err))
// 	}

// 	// Default max attempts is 20, but we hit this sometimes
// 	err = svc.WaitUntilBucketExistsWithContext(aws.BackgroundContext(), &s3.HeadBucketInput{
// 		Bucket: aws.String(bucket),
// 	}, request.WithWaiterMaxAttempts(50))
// 	if err != nil {
// 		return errors.WriteError(err.Error())
// 	}
// 	return nil
// }

// func DeleteS3Bucket(region, bucket string) (err error) {
// 	sess, err := session.NewSession(&aws.Config{
// 		Region:                        aws.String(region),
// 		CredentialsChainVerboseErrors: aws.Bool(true),
// 	})
// 	if err != nil {
// 		return fmt.Errorf("Failed to connect to S3: %v", err)
// 	}
// 	svc := s3.New(sess)

// 	iter := s3manager.NewDeleteListIterator(svc, &s3.ListObjectsInput{
// 		Bucket: aws.String(bucket),
// 	})

// 	if err := s3manager.NewBatchDeleteWithClient(svc).Delete(aws.BackgroundContext(), iter); err != nil {
// 		return errors.WriteError(fmt.Sprintf("Unable to delete objects from bucket %q, %v", bucket, err))
// 	}
// 	_, err = svc.DeleteBucket(&s3.DeleteBucketInput{
// 		Bucket: aws.String(bucket),
// 	})
// 	if err != nil {
// 		return errors.WriteError(fmt.Sprintf("Unable to delete bucket %q, %v", bucket, err))
// 	}
// 	return nil
// }

func (s *MinioRepository) listRecursive(results chan<- ListResult, dir string, filter func(string) bool) {
	prefix := filepath.Join(s.root, dir)
	// prefixes must end with / and must not end with /
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	prefix = strings.TrimPrefix(prefix, "/")
	opts := minio.ListObjectsOptions{
		UseV1:     true,
		Prefix:    prefix,
		Recursive: true,
		MaxKeys:   int(1000),
	}
	resultCh := s.client.ListObjects(context.TODO(), s.bucketName, opts)
	for r := range resultCh {
		if r.Err != nil {
			results <- ListResult{Error: fmt.Errorf("Failed to list objects in s3://%s: %s", s.bucketName, r.Err.Error())}
			continue
		}

		key := r.Key
		if s.root != "" {
			key = strings.TrimPrefix(strings.TrimPrefix(key, s.root), "/")
		}
		if filter(key) {
			// If S3 gives us an empty/bad etag, then make it blank and cause sync instead of throwing error
			// Also, the etag includes quotes for some reason
			md5, _ := hex.DecodeString(strings.Replace(r.ETag, "\"", "", -1))
			results <- ListResult{Path: key, MD5: md5}
		}
	}
	// err := s.svc.ListObjectsPages(&s3.ListObjectsInput{
	// 	Bucket:  aws.String(s.bucketName),
	// 	Prefix:  aws.String(prefix),
	// 	MaxKeys: aws.Int64(1000),
	// }, func(page *s3.ListObjectsOutput, lastPage bool) bool {
	// 	for _, value := range page.Contents {
	// 		key := *value.Key
	// 		if s.root != "" {
	// 			key = strings.TrimPrefix(strings.TrimPrefix(key, s.root), "/")
	// 		}
	// 		if filter(key) {
	// 			// If S3 gives us an empty/bad etag, then make it blank and cause sync instead of throwing error
	// 			// Also, the etag includes quotes for some reason
	// 			md5, _ := hex.DecodeString(strings.Replace(*value.ETag, "\"", "", -1))
	// 			results <- ListResult{Path: key, MD5: md5}
	// 		}
	// 	}
	// return true
	// })
	// if err != nil {
	// results <- ListResult{Error: fmt.Errorf("Failed to list objects in s3://%s: %s", s.bucketName, err)}
	// }
	close(results)
}

// func discoverBucketRegion(bucket string) (string, error) {
// 	sess := session.Must(session.NewSession(&aws.Config{}))
// 	ctx := context.Background()
// 	region, err := s3manager.GetBucketRegion(ctx, sess, bucket, global.S3Region)
// 	if err != nil {
// 		return "", err
// 	}
// 	return region, nil
// }

// func getBucketRegionOrCreateBucket(bucket string) (string, error) {
// 	// TODO (bfirsh): cache this
// 	region, err := discoverBucketRegion(bucket)
// 	if err != nil {
// 		if aerr, ok := err.(awserr.Error); ok {
// 			// The real check for this is `aerr.Code() == s3.ErrCodeNoSuchBucket` but GetBucketRegion doesnt return right error
// 			if strings.Contains(aerr.Error(), "NotFound") {
// 				// TODO (bfirsh): report to use that this is being created, in a way that is compatible with shared library
// 				if err := CreateS3Bucket(global.S3Region, bucket); err != nil {
// 					return "", fmt.Errorf("Error creating bucket: %v", err)
// 				}
// 				return region, nil
// 			}
// 		}
// 		return "", fmt.Errorf("Failed to discover AWS region for bucket %s: %s", bucket, err)
// 	}
// 	return region, nil
// }
