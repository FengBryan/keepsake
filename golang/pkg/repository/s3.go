package repository

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"golang.org/x/sync/errgroup"

	"github.com/replicate/keepsake/golang/pkg/concurrency"
	"github.com/replicate/keepsake/golang/pkg/console"
	"github.com/replicate/keepsake/golang/pkg/errors"
	"github.com/replicate/keepsake/golang/pkg/files"
	"github.com/replicate/keepsake/golang/pkg/global"
)

type S3Repository struct {
	bucketName string
	root       string
	sess       *session.Session
	svc        *s3.S3
}

func NewS3Repository(bucket, root string) (*S3Repository, error) {
	region, err := getBucketRegionOrCreateBucket(bucket)
	if err != nil {
		return nil, err
	}

	s := &S3Repository{
		bucketName: bucket,
		root:       root,
	}
	s.sess, err = session.NewSession(&aws.Config{
		Region:                        aws.String(region),
		CredentialsChainVerboseErrors: aws.Bool(true),
	})
	if err != nil {
		return nil, errors.RepositoryConfigurationError(fmt.Sprintf("Failed to connect to S3: %s", err))
	}
	s.svc = s3.New(s.sess)

	return s, nil
}

func (s *S3Repository) RootURL() string {
	ret := "s3://" + s.bucketName
	if s.root != "" {
		ret += "/" + s.root
	}
	return ret
}

// Get data at path
func (s *S3Repository) Get(path string) ([]byte, error) {
	key := filepath.Join(s.root, path)
	obj, err := s.svc.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == s3.ErrCodeNoSuchKey {
				return nil, errors.DoesNotExist(fmt.Sprintf("Get: path does not exist: %v", path))
			}
		}
		return nil, errors.ReadError(fmt.Sprintf("Failed to read %s/%s: %s", s.RootURL(), path, err))
	}
	body, err := io.ReadAll(obj.Body)
	if err != nil {
		return nil, errors.ReadError(fmt.Sprintf("Failed to read body from %s/%s: %s", s.RootURL(), path, err))
	}
	return body, nil
}

func (s *S3Repository) Delete(path string) error {
	console.Debug("Deleting %s/%s...", s.RootURL(), path)
	key := filepath.Join(s.root, path)
	iter := s3manager.NewDeleteListIterator(s.svc, &s3.ListObjectsInput{
		Bucket: &s.bucketName,
		Prefix: &key,
	})
	if err := s3manager.NewBatchDeleteWithClient(s.svc).Delete(aws.BackgroundContext(), iter); err != nil {
		return errors.WriteError(fmt.Sprintf("Failed to delete %s/%s: %v", s.RootURL(), path, err))
	}
	return nil
}

// Put data at path
func (s *S3Repository) Put(path string, data []byte) error {
	key := filepath.Join(s.root, path)
	uploader := s3manager.NewUploader(s.sess)
	_, err := uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return errors.WriteError(fmt.Sprintf("Unable to upload to %s/%s: %v", s.RootURL(), path, err))
	}
	return nil
}

func (s *S3Repository) PutPath(localPath string, destPath string) error {
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

			uploader := s3manager.NewUploader(s.sess)
			_, err = uploader.Upload(&s3manager.UploadInput{
				Bucket: aws.String(s.bucketName),
				Key:    aws.String(file.Dest),
				Body:   bytes.NewReader(data),
			})
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

func (s *S3Repository) PutPathTar(localPath, tarPath, includePath string) error {
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
		uploader := s3manager.NewUploader(s.sess)
		_, err := uploader.Upload(&s3manager.UploadInput{
			Bucket: aws.String(s.bucketName),
			Key:    aws.String(key),
			Body:   reader,
		})
		return err
	})
	if err := errs.Wait(); err != nil {
		return errors.WriteError(err.Error())
	}
	return nil
}

// GetPath recursively copies repoDir to localDir
func (s *S3Repository) GetPath(remoteDir string, localDir string) error {
	prefix := filepath.Join(s.root, remoteDir)
	iter := new(s3manager.DownloadObjectsIterator)
	files := []*os.File{}
	defer func() {
		for _, f := range files {
			if err := f.Close(); err != nil {
				console.Warn("Failed to close file %s", f.Name())
			}
		}
	}()

	keys := []*string{}
	err := s.svc.ListObjectsV2PagesWithContext(aws.BackgroundContext(), &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucketName),
		Prefix: aws.String(prefix),
	}, func(output *s3.ListObjectsV2Output, last bool) bool {
		for _, object := range output.Contents {
			keys = append(keys, object.Key)
		}
		return true
	})
	if err != nil {
		return errors.ReadError(fmt.Sprintf("Failed to list objects in s3://%s/%s: %v", s.bucketName, prefix, err))
	}

	for _, key := range keys {
		relPath, err := filepath.Rel(prefix, *key)
		if err != nil {
			return fmt.Errorf("Failed to determine directory of %s relative to %s: %v", *key, prefix, err)
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

		console.Debug("Downloading %s to %s", *key, localPath)

		iter.Objects = append(iter.Objects, s3manager.BatchDownloadObject{
			Object: &s3.GetObjectInput{
				Bucket: aws.String(s.bucketName),
				Key:    key,
			},
			Writer: f,
		})
		files = append(files, f)
	}

	downloader := s3manager.NewDownloader(s.sess)
	if err := downloader.DownloadWithIterator(aws.BackgroundContext(), iter); err != nil {
		return errors.ReadError(fmt.Sprintf("Failed to download s3://%s/%s to %s", s.bucketName, prefix, localDir))
	}
	return nil
}

func (s *S3Repository) GetPathTar(tarPath, localPath string) error {
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

func (s *S3Repository) GetPathItemTar(tarPath, itemPath, localPath string) error {
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

func (s *S3Repository) ListRecursive(results chan<- ListResult, dir string) {
	s.listRecursive(results, dir, func(_ string) bool { return true })
}

func (s *S3Repository) MatchFilenamesRecursive(results chan<- ListResult, folder string, filename string) {
	s.listRecursive(results, folder, func(key string) bool {
		return filepath.Base(key) == filename
	})
}

// List files in a path non-recursively
func (s *S3Repository) List(dir string) ([]string, error) {
	results := []string{}
	prefix := filepath.Join(s.root, dir)

	// prefixes must end with / and must not end with /
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	prefix = strings.TrimPrefix(prefix, "/")

	err := s.svc.ListObjectsPages(&s3.ListObjectsInput{
		Bucket:    aws.String(s.bucketName),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
		MaxKeys:   aws.Int64(1000),
	}, func(page *s3.ListObjectsOutput, lastPage bool) bool {
		for _, value := range page.Contents {
			key := *value.Key
			if s.root != "" {
				key = strings.TrimPrefix(strings.TrimPrefix(key, s.root), "/")
			}
			results = append(results, key)
		}
		return true
	})
	if err != nil {
		return nil, errors.ReadError(err.Error())
	}
	return results, nil
}

func (s *S3Repository) ListTarFile(tarPath string) ([]string, error) {
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

func CreateS3Bucket(region, bucket string) (err error) {
	sess, err := session.NewSession(&aws.Config{
		Region:                        aws.String(region),
		CredentialsChainVerboseErrors: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("Failed to connect to S3: %w", err)
	}
	svc := s3.New(sess)

	_, err = svc.CreateBucket(&s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return errors.WriteError(fmt.Sprintf("Unable to create bucket %q, %v", bucket, err))
	}

	// Default max attempts is 20, but we hit this sometimes
	err = svc.WaitUntilBucketExistsWithContext(aws.BackgroundContext(), &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	}, request.WithWaiterMaxAttempts(50))
	if err != nil {
		return errors.WriteError(err.Error())
	}
	return nil
}

func DeleteS3Bucket(region, bucket string) (err error) {
	sess, err := session.NewSession(&aws.Config{
		Region:                        aws.String(region),
		CredentialsChainVerboseErrors: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("Failed to connect to S3: %v", err)
	}
	svc := s3.New(sess)

	iter := s3manager.NewDeleteListIterator(svc, &s3.ListObjectsInput{
		Bucket: aws.String(bucket),
	})

	if err := s3manager.NewBatchDeleteWithClient(svc).Delete(aws.BackgroundContext(), iter); err != nil {
		return errors.WriteError(fmt.Sprintf("Unable to delete objects from bucket %q, %v", bucket, err))
	}
	_, err = svc.DeleteBucket(&s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return errors.WriteError(fmt.Sprintf("Unable to delete bucket %q, %v", bucket, err))
	}
	return nil
}

func (s *S3Repository) listRecursive(results chan<- ListResult, dir string, filter func(string) bool) {
	prefix := filepath.Join(s.root, dir)
	// prefixes must end with / and must not end with /
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	prefix = strings.TrimPrefix(prefix, "/")

	err := s.svc.ListObjectsPages(&s3.ListObjectsInput{
		Bucket:  aws.String(s.bucketName),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int64(1000),
	}, func(page *s3.ListObjectsOutput, lastPage bool) bool {
		for _, value := range page.Contents {
			key := *value.Key
			if s.root != "" {
				key = strings.TrimPrefix(strings.TrimPrefix(key, s.root), "/")
			}
			if filter(key) {
				// If S3 gives us an empty/bad etag, then make it blank and cause sync instead of throwing error
				// Also, the etag includes quotes for some reason
				md5, _ := hex.DecodeString(strings.Replace(*value.ETag, "\"", "", -1))
				results <- ListResult{Path: key, MD5: md5}
			}
		}
		return true
	})
	if err != nil {
		results <- ListResult{Error: fmt.Errorf("Failed to list objects in s3://%s: %s", s.bucketName, err)}
	}
	close(results)
}

func discoverBucketRegion(bucket string) (string, error) {
	sess := session.Must(session.NewSession(&aws.Config{}))
	ctx := context.Background()
	region, err := s3manager.GetBucketRegion(ctx, sess, bucket, global.S3Region)
	if err != nil {
		return "", err
	}
	return region, nil
}

func getBucketRegionOrCreateBucket(bucket string) (string, error) {
	// TODO (bfirsh): cache this
	region, err := discoverBucketRegion(bucket)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			// The real check for this is `aerr.Code() == s3.ErrCodeNoSuchBucket` but GetBucketRegion doesnt return right error
			if strings.Contains(aerr.Error(), "NotFound") {
				// TODO (bfirsh): report to use that this is being created, in a way that is compatible with shared library
				if err := CreateS3Bucket(global.S3Region, bucket); err != nil {
					return "", fmt.Errorf("Error creating bucket: %v", err)
				}
				return region, nil
			}
		}
		return "", fmt.Errorf("Failed to discover AWS region for bucket %s: %s", bucket, err)
	}
	return region, nil
}
