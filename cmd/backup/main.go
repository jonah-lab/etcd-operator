package backup

import (
	"context"
	"flag"
	"fmt"
	"github.com/cnych/etcd-operator/api/v1alpha1"
	"github.com/cnych/etcd-operator/pkg/file"
	"github.com/coreos/etcd/clientv3"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.etcd.io/etcd/clientv3/snapshot"
	"os"
	"path/filepath"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"time"
)

func logErr(log logr.Logger, err error, message string) error {
	log.Error(err, message)
	return fmt.Errorf("%s: %s", message, err)
}

func main() {
	var (
		backupTempDir          string
		etcdURL                string
		backupURL              string
		etcdDialTimeoutSeconds int64
		timeoutSeconds         int64
	)

	flag.StringVar(&backupTempDir, "backup-tmp-dir", os.TempDir(), "The directory to temporarily place backups before they are uploaded to their destination.")
	flag.StringVar(&etcdURL, "etcd-url", "http://localhost:2379", "URL for etcd.")
	flag.Int64Var(&etcdDialTimeoutSeconds, "etcd-dial-timeout-seconds", 5, "Timeout, in seconds, for dialing the Etcd API.")
	flag.StringVar(&backupURL, "backup-url", "", "URL for the backup storage.")
	flag.Int64Var(&timeoutSeconds, "timeout-seconds", 60, "Timeout, in seconds, of the whole restore operation.")
	flag.Parse()

	zapLogger := zap.NewRaw(zap.UseDevMode(true))
	ctrl.SetLogger(zapr.NewLogger(zapLogger))

	storageType, bucketName, objectName, err := file.ParseBackupURL(backupURL)

	log := ctrl.Log.WithName("backup-agent")
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second*time.Duration(timeoutSeconds))
	defer ctxCancel()

	log.Info("Connecting to Etcd and getting snapshot")
	localPath := filepath.Join(backupTempDir, "snapshot.db")
	etcdClient := snapshot.NewV3(zapLogger.Named("etcd-client"))

	err = etcdClient.Save(
		ctx,
		clientv3.Config{
			Endpoints:   []string{etcdURL},
			DialTimeout: time.Second * time.Duration(etcdDialTimeoutSeconds),
		},
		localPath,
	)
	if err != nil {
		panic(logErr(log, err, "failed to get etcd snapshot"))
	}
	switch storageType {
	case string(v1alpha1.BackupStorageTypeS3):
		size, err := handleS3(ctx, bucketName, objectName, localPath)
		if err != nil {
			panic(logErr(log, err, "failed to upload backup etcd"))
		}

		log.WithValues("upload-size", size).Info("Backup completed")

	case string(v1alpha1.BackupStorageTypeOSS):
	default:
		panic(logErr(log, fmt.Errorf("storage type error"), fmt.Sprintf("unknown StorageType: %v", storageType)))
	}

}

func handleS3(ctx context.Context, bucketName, objectName, localPath string) (int64, error) {
	//endpoint := "play.min.io"
	//accessKeyID := "Q3AM3UQ867SPQQA43P2F"
	//secretAccessKey := "zuf+tfteSlswRu7BJ86wekitnifILbZam1KYY3TG"
	endpoint := os.Getenv("ENDPOINT")
	accessKeyID := os.Getenv("MINIO_ACCESS_KEY")
	secretAccessKey := os.Getenv("MINIO_SECRET_KEY")
	s3Uploader := file.NewS3Uploader(endpoint, accessKeyID, secretAccessKey)
	return s3Uploader.Upload(ctx, bucketName, objectName, localPath)
}
