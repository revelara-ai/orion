package sandbox

// KubernetesPodCreator is the production K8sPodCreator backed by
// k8s.io/client-go. Replaces the InMemoryPodCreator wired by tests
// in orion-e43.
//
// AlreadyExists handling per SPEC §11.1: the workspace_key UNIQUE
// constraint at the DB layer plus the pod-name UNIQUE constraint at
// the Kubernetes API together make pod creation idempotent. When the
// Kubernetes API reports an AlreadyExists error for our pod name we
// translate it to a success-not-error result so the Conductor's
// idempotent-retry path lands cleanly.

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// KubernetesPodCreator implements K8sPodCreator against a live cluster.
type KubernetesPodCreator struct {
	Client      kubernetes.Interface
	WorkerImage string
	// CPURequest / MemoryRequest / CPULimit / MemoryLimit cap the worker
	// pod's resources per SPEC §10.3.
	CPURequest    string
	MemoryRequest string
	CPULimit      string
	MemoryLimit   string
}

// NewKubernetesPodCreator builds a creator with reasonable defaults.
// Caller MUST set Client to a kubernetes.Interface bound to the
// target cluster.
func NewKubernetesPodCreator(client kubernetes.Interface, workerImage string) *KubernetesPodCreator {
	return &KubernetesPodCreator{
		Client:        client,
		WorkerImage:   workerImage,
		CPURequest:    "500m",
		MemoryRequest: "512Mi",
		CPULimit:      "2",
		MemoryLimit:   "4Gi",
	}
}

// Create materializes a worker pod for the given intent. The pod
// carries the workspace_key as a label so future reconciliation and
// the Lookout can locate the canonical pod by workspace_key.
// AlreadyExists translates to Created=false success.
func (c *KubernetesPodCreator) Create(ctx context.Context, intent PodCreateIntent) (PodCreateResult, error) {
	if c.Client == nil {
		return PodCreateResult{}, errors.New("sandbox: nil kubernetes.Interface")
	}
	if intent.WorkspaceKey == "" {
		return PodCreateResult{}, errors.New("sandbox: WorkspaceKey is required")
	}
	if intent.Namespace == "" || intent.PodName == "" {
		return PodCreateResult{}, errors.New("sandbox: Namespace and PodName are required")
	}
	image := intent.ContainerImage
	if image == "" {
		image = c.WorkerImage
	}
	envVars := make([]corev1.EnvVar, 0, len(intent.Env))
	for k, v := range intent.Env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      intent.PodName,
			Namespace: intent.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":          "orion-worker",
				"orion.revelara.ai/workspace-key": intent.WorkspaceKey,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: boolPtr(false),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: boolPtr(true),
			},
			Containers: []corev1.Container{
				{
					Name:  "worker",
					Image: image,
					Env:   envVars,
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: boolPtr(false),
						ReadOnlyRootFilesystem:   boolPtr(true),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(c.CPURequest),
							corev1.ResourceMemory: resource.MustParse(c.MemoryRequest),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(c.CPULimit),
							corev1.ResourceMemory: resource.MustParse(c.MemoryLimit),
						},
					},
				},
			},
		},
	}
	got, err := c.Client.CoreV1().Pods(intent.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Idempotent: prior leader already provisioned this pod.
			return PodCreateResult{
				Created:      false,
				Namespace:    intent.Namespace,
				PodName:      intent.PodName,
				WorkspaceKey: intent.WorkspaceKey,
			}, nil
		}
		return PodCreateResult{}, fmt.Errorf("sandbox: create pod: %w", err)
	}
	return PodCreateResult{
		Created:      true,
		Namespace:    intent.Namespace,
		PodName:      got.Name,
		WorkspaceKey: intent.WorkspaceKey,
	}, nil
}

func boolPtr(b bool) *bool { return &b }
