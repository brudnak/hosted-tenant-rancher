package test

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/brudnak/hosted-tenant-rancher/tools/hcl"
	"github.com/spf13/viper"
)

var s3UploadExcludedNames = map[string]struct{}{
	".terraform":               {},
	"terraform.tfstate":        {},
	"terraform.tfstate.backup": {},
}

func createAWSVar() {
	if err := ensureConfigLoaded(); err != nil {
		log.Println("error reading config:", err)
		return
	}

	hcl.GenAwsVar(
		viper.GetString("tf_vars.aws_prefix"),
		viper.GetString("tf_vars.aws_vpc"),
		viper.GetString("tf_vars.aws_subnet_a"),
		viper.GetString("tf_vars.aws_subnet_b"),
		viper.GetString("tf_vars.aws_subnet_c"),
		viper.GetString("tf_vars.aws_ami"),
		viper.GetString("tf_vars.aws_subnet_id"),
		viper.GetString("tf_vars.aws_security_group_id"),
		viper.GetString("tf_vars.aws_pem_key_name"),
		viper.GetString("tf_vars.aws_rds_password"),
		viper.GetString("tf_vars.aws_route53_fqdn"),
		viper.GetString("tf_vars.aws_ec2_instance_type"),
	)
}

func checkS3ObjectExists(item string) error {
	if err := ensureConfigLoaded(); err != nil {
		return err
	}

	sess, _ := session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region"))},
	)

	bucket := viper.GetString("s3.bucket")

	svc := s3.New(sess)

	_, err := svc.HeadObject(&s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(item)})
	if err != nil {
		var aErr awserr.Error
		if errors.As(err, &aErr) {
			switch aErr.Code() {
			case s3.ErrCodeNoSuchKey, "NotFound":
				return nil
			}
		}
		return err
	}

	log.Fatalf("A tfstate file already exists in bucket %s. Please clean up the old hosted/tenant environment before creating a new one.", bucket)
	return nil
}

func uploadFolderToS3(folderPath string) error {
	if err := ensureConfigLoaded(); err != nil {
		return fmt.Errorf("error reading config: %w", err)
	}

	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region")),
	})
	if err != nil {
		return fmt.Errorf("error creating AWS session: %w", err)
	}

	svc := s3.New(sess)

	bucket := viper.GetString("s3.bucket")

	err = filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if _, excluded := s3UploadExcludedNames[info.Name()]; excluded {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("error opening file %s: %w", path, err)
		}
		defer func(file *os.File) {
			err := file.Close()
			if err != nil {

			}
		}(file)

		key, err := filepath.Rel(folderPath, path)
		if err != nil {
			return fmt.Errorf("error getting relative path: %w", err)
		}
		key = strings.ReplaceAll(key, string(os.PathSeparator), "/")

		_, err = svc.PutObject(&s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   file,
		})
		if err != nil {
			return fmt.Errorf("error uploading file %s: %w", path, err)
		}

		log.Printf("Successfully uploaded %s to %s\n", path, bucket+"/"+key)
		return nil
	})

	if err != nil {
		return fmt.Errorf("error walking through folder: %w", err)
	}

	return nil
}

func clearS3Bucket(bucketName string) error {
	if err := ensureConfigLoaded(); err != nil {
		return fmt.Errorf("error reading config: %w", err)
	}

	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region")),
	})
	if err != nil {
		return fmt.Errorf("error creating AWS session: %w", err)
	}

	svc := s3.New(sess)

	err = svc.ListObjectsV2Pages(&s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
	}, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		var objectsToDelete []*s3.ObjectIdentifier
		for _, obj := range page.Contents {
			objectsToDelete = append(objectsToDelete, &s3.ObjectIdentifier{
				Key: obj.Key,
			})
		}

		if len(objectsToDelete) > 0 {
			_, err := svc.DeleteObjects(&s3.DeleteObjectsInput{
				Bucket: aws.String(bucketName),
				Delete: &s3.Delete{
					Objects: objectsToDelete,
					Quiet:   aws.Bool(false),
				},
			})
			if err != nil {
				fmt.Printf("Error deleting objects: %v\n", err)
				return false
			}
		}

		return true
	})

	if err != nil {
		return fmt.Errorf("error clearing bucket: %w", err)
	}

	fmt.Printf("Successfully cleared all contents from bucket: %s\n", bucketName)
	return nil
}
