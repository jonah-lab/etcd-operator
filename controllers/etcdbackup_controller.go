/*
Copyright 2021 jonah-lab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"html/template"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	etcdv1alpha1 "github.com/cnych/etcd-operator/api/v1alpha1"
)

// EtcdBackupReconciler reconciles a EtcdBackup object
type EtcdBackupReconciler struct {
	BackupImage string
	Record      record.EventRecorder
	client.Client
	Scheme *runtime.Scheme
}

type backupState struct {
	backup  *etcdv1alpha1.EtcdBackup // EtcdBackup 对象本身
	actual  *backupStateContainer    // 真实的状态
	desired *backupStateContainer    // 期望的状态
}

// backupStateContainer 包含 EtcdBackup 的状态
type backupStateContainer struct {
	pod *corev1.Pod
}

func (r *EtcdBackupReconciler) setStateActual(ctx context.Context, state *backupState) error {
	var actual backupStateContainer
	key := client.ObjectKey{
		Name:      state.backup.Name,
		Namespace: state.backup.Namespace,
	}
	actual.pod = &corev1.Pod{}
	if err := r.Get(ctx, key, actual.pod); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("getting pod error:%s", err)
		}
		actual.pod = nil
	}
	state.actual = &actual
	return nil
}

func (r *EtcdBackupReconciler) getState(ctx context.Context, req ctrl.Request) (*backupState, error) {
	var state backupState
	// 获取 EtcdBackup 对象
	state.backup = &etcdv1alpha1.EtcdBackup{}
	if err := r.Get(ctx, req.NamespacedName, state.backup); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, fmt.Errorf("getting backup error:%s", err)
		}
		// 被删除了则直接忽略
		state.backup = nil
		return &state, nil
	}
	if err := r.setStateActual(ctx, &state); err != nil {
		return nil, fmt.Errorf("setting desired state error:%s", err)
	}
	if err := r.setStateDesired(&state); err != nil {
		return nil, fmt.Errorf("setting desired state error: %s", err)
	}
	return &state, nil
}

// setStateDesired 用于设置 backupState 的期望状态（根据 EtcdBackup 对象）
func (r *EtcdBackupReconciler) setStateDesired(state *backupState) error {
	var desired backupStateContainer
	// 创建一个管理的 Pod 用于执行备份操作
	pod, err := podForBackup(state.backup, r.BackupImage)
	if err != nil {
		return fmt.Errorf("computing pod for backup error: %q", err)
	}
	// 配置 controller reference
	if err := controllerutil.SetControllerReference(state.backup, pod, r.Scheme); err != nil {
		return fmt.Errorf("setting pod controller reference error : %s", err)
	}
	desired.pod = pod
	// 获得期望的对象
	state.desired = &desired
	return nil
}

func podForBackup(backup *etcdv1alpha1.EtcdBackup, image string) (*corev1.Pod, error) {
	var secretRef *corev1.SecretEnvSource
	var backupEndpoint, backupURL string
	if backup.Spec.StorageType == etcdv1alpha1.BackupStorageTypeS3 {
		tmpl, err := template.New("templete").Parse(backup.Spec.S3.Path)
		if err != nil {
			return nil, err
		}
		// 解析成备份的地址
		var objectURL strings.Builder
		if err := tmpl.Execute(&objectURL, backupURL); err != nil {
			return nil, err
		}
		// format s3://my-bucket/{{ .NameSpace }}/{{ .Name }}/{{ .CreationTimeStamp }}/snapshot.db
		backupURL = fmt.Sprintf("%s://%s", backup.Spec.StorageType, objectURL.String())
		backupEndpoint = backup.Spec.S3.Endpoint
		secretRef = &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: backup.Spec.S3.Secret,
			},
		}
	} else {
		backupURL = fmt.Sprintf("%s://%s", backup.Spec.StorageType, backup.Spec.OSS.Path)
		backupEndpoint = backup.Spec.OSS.Endpoint
		secretRef = &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: backup.Spec.OSS.Secret,
			},
		}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backup.Name,
			Namespace: backup.Namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "backup-agent",
					Image: image,
					Args: []string{
						"--etcd-url", backup.Spec.EtcdUrl,
						"--backup-url", backupURL,
					},
					Env: []corev1.EnvVar{
						{
							Name:  "ENDPOINT",
							Value: backupEndpoint,
						},
					},
					EnvFrom: []corev1.EnvFromSource{
						{
							SecretRef: secretRef,
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("50Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("50Mi"),
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}, nil
}

//+kubebuilder:rbac:groups=etcd.jonah-lab.io,resources=etcdbackups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=etcd.jonah-lab.io,resources=etcdbackups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=etcd.jonah-lab.io,resources=etcdbackups/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the EtcdBackup object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *EtcdBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	curLog := log.FromContext(ctx)
	printLog := curLog.WithValues("etcdBackup", req.NamespacedName)
	// get backup state
	state, err := r.getState(ctx, req)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 根据状态来判断下一步要执行的动作
	var action Action

	switch {
	case state.backup == nil: // 被删除了
		printLog.Info("Backup Object not found. Ignoring.")
	case !state.backup.DeletionTimestamp.IsZero(): // 标记为了删除
		printLog.Info("Backup Object has been deleted. Ignoring.")
	case state.backup.Status.Phase == "": // 开始备份，更新状态
		printLog.Info("Backup Staring. Updating status.")
		newBackup := state.backup.DeepCopy()                                            // 深拷贝一份
		newBackup.Status.Phase = etcdv1alpha1.EtcdBackupPhaseBackingUp                  // 更新状态为备份中
		action = &PatchStatus{client: r.Client, original: state.backup, new: newBackup} // 下一步要执行的动作
	case state.backup.Status.Phase == etcdv1alpha1.EtcdBackupPhaseFailed: // 备份失败
		printLog.Info("Backup has failed. Ignoring.")
	case state.backup.Status.Phase == etcdv1alpha1.EtcdBackupPhaseCompleted: // 备份完成
		printLog.Info("Backup has completed. Ignoring.")
	case state.actual.pod == nil: // 当前还没有备份的 Pod
		printLog.Info("Backup Pod does not exists. Creating.")
		action = &CreateObject{client: r.Client, obj: state.desired.pod} // 下一步要执行的动作
		r.Record.Event(state.backup, corev1.EventTypeNormal, "SuccessfulCreated",
			fmt.Sprintf("create pod:%s successfull!", state.desired.pod.Name))
	case state.actual.pod.Status.Phase == corev1.PodFailed: // 备份Pod执行失败
		printLog.Info("Backup Pod failed. Updating status.")
		newBackup := state.backup.DeepCopy()
		newBackup.Status.Phase = etcdv1alpha1.EtcdBackupPhaseFailed
		action = &PatchStatus{client: r.Client, original: state.backup, new: newBackup} // 下一步更新状态为失败
		r.Record.Event(state.backup, corev1.EventTypeWarning, "BackupFailed", "Backup failed. See backup pod logs for details.")
	case state.actual.pod.Status.Phase == corev1.PodSucceeded: // 备份Pod执行完成
		printLog.Info("Backup Pod succeeded. Updating status.")
		newBackup := state.backup.DeepCopy()
		newBackup.Status.Phase = etcdv1alpha1.EtcdBackupPhaseCompleted
		action = &PatchStatus{client: r.Client, original: state.backup, new: newBackup} // 下一步更新状态为完成
		r.Record.Event(state.backup, corev1.EventTypeNormal, "BackupSucceeded", "Backup completed successfully")
	}

	// 执行动作
	if action != nil {
		if err := action.Execute(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("executing action error: %s", err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EtcdBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&etcdv1alpha1.EtcdBackup{}).
		Complete(r)
}
