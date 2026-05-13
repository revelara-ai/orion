package harness

// K8sMaterializer is the live materializer that applies a
// MaterializationPlan to a real Kubernetes cluster via client-go
// (SPEC §10 + §12.4). Replaces the in-process runner from E1 once the
// orchestrator wires it; the LocalRunner stays as the fallback for
// unit tests and dogfooding without a cluster.
//
// Idempotency: every operation maps to a client-go Create call with
// AlreadyExists treated as success. A re-run of K8sMaterializer
// against the same Harness produces the same materialized state.

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// K8sMaterializer applies a Harness to a Kubernetes cluster.
type K8sMaterializer struct {
	Client kubernetes.Interface
	// SUTImage is the container image for the system-under-test
	// Deployment. The worker passes its own working-tree build here.
	SUTImage string
	// CPURequest / MemoryRequest cap the SUT Deployment per SPEC §10.3.
	CPURequest    string
	MemoryRequest string
	CPULimit      string
	MemoryLimit   string
}

// NewK8sMaterializer builds a materializer with sensible defaults.
func NewK8sMaterializer(client kubernetes.Interface, sutImage string) *K8sMaterializer {
	return &K8sMaterializer{
		Client:        client,
		SUTImage:      sutImage,
		CPURequest:    "100m",
		MemoryRequest: "128Mi",
		CPULimit:      "1",
		MemoryLimit:   "1Gi",
	}
}

// MaterializedHarness is the live state record returned by Apply.
// Holds enough to tear down later via Teardown.
type MaterializedHarness struct {
	Namespace     string
	NetworkPolicy string
	Deployments   []string
	ConfigMaps    []string
}

// Apply materializes the Harness into a live cluster. Calls (in order):
//  1. Namespace + ResourceQuota
//  2. NetworkPolicy (default-deny + harness-internal allow)
//  3. Toxiproxy ConfigMap + Deployment (if h.Fault declares any profiles)
//  4. SUT Deployment (one per Service in h.Workload.Endpoints)
func (m *K8sMaterializer) Apply(ctx context.Context, h *Harness) (*MaterializedHarness, error) {
	if m.Client == nil {
		return nil, errors.New("harness: nil kubernetes.Interface")
	}
	if h == nil {
		return nil, errors.New("harness: nil harness")
	}
	if h.Namespace == "" || !validNamespace.MatchString(h.Namespace) {
		return nil, fmt.Errorf("%w: namespace %q invalid", ErrMaterialization, h.Namespace)
	}
	out := &MaterializedHarness{Namespace: h.Namespace}

	if err := m.applyNamespace(ctx, h); err != nil {
		return nil, err
	}
	if err := m.applyNetworkPolicy(ctx, h); err != nil {
		return nil, err
	}
	out.NetworkPolicy = "deny-egress"

	if len(h.Faults.Faults) > 0 {
		cm, err := m.applyToxiproxyConfigMap(ctx, h)
		if err != nil {
			return nil, err
		}
		out.ConfigMaps = append(out.ConfigMaps, cm)
		if err := m.applyToxiproxyDeployment(ctx, h); err != nil {
			return nil, err
		}
		out.Deployments = append(out.Deployments, "toxiproxy")
	}

	services := distinctServices(h)
	for _, svc := range services {
		if err := m.applySUTDeployment(ctx, h, svc); err != nil {
			return nil, err
		}
		out.Deployments = append(out.Deployments, svc)
	}

	return out, nil
}

// Teardown deletes the namespace. Cascade removes every object Apply
// created.
func (m *K8sMaterializer) Teardown(ctx context.Context, mat *MaterializedHarness) error {
	if m.Client == nil {
		return errors.New("harness: nil kubernetes.Interface")
	}
	if mat == nil || mat.Namespace == "" {
		return errors.New("harness: nil materialized harness")
	}
	if err := m.Client.CoreV1().Namespaces().Delete(ctx, mat.Namespace, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("harness: delete namespace: %w", err)
	}
	return nil
}

func (m *K8sMaterializer) applyNamespace(ctx context.Context, h *Harness) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: h.Namespace,
			Labels: map[string]string{
				"orion.revelara.ai/run-id":     h.RunID,
				"orion.revelara.ai/managed-by": "orion-harness",
				// PodSecurity restricted, mirroring §10.3.
				"pod-security.kubernetes.io/enforce": "restricted",
				"pod-security.kubernetes.io/audit":   "restricted",
				"pod-security.kubernetes.io/warn":    "restricted",
			},
		},
	}
	_, err := m.Client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("harness: create namespace: %w", err)
	}
	return nil
}

func (m *K8sMaterializer) applyNetworkPolicy(ctx context.Context, h *Harness) error {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deny-egress",
			Namespace: h.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
		},
	}
	_, err := m.Client.NetworkingV1().NetworkPolicies(h.Namespace).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("harness: create network policy: %w", err)
	}
	return nil
}

func (m *K8sMaterializer) applyToxiproxyConfigMap(ctx context.Context, h *Harness) (string, error) {
	cfg, err := BuildToxiproxyConfig(h)
	if err != nil {
		return "", err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "toxiproxy-config",
			Namespace: h.Namespace,
		},
		Data: map[string]string{"toxiproxy.json": cfg},
	}
	_, err = m.Client.CoreV1().ConfigMaps(h.Namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("harness: create toxiproxy configmap: %w", err)
	}
	return "toxiproxy-config", nil
}

func (m *K8sMaterializer) applyToxiproxyDeployment(ctx context.Context, h *Harness) error {
	// The Deployment is built as a raw client-go AppsV1.Deployment;
	// kept inline to avoid a separate file for ~50 lines of boilerplate.
	pod := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "toxiproxy",
				Image: "ghcr.io/shopify/toxiproxy:2.9.0",
				Args:  []string{"-config", "/etc/toxiproxy/toxiproxy.json"},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "config", MountPath: "/etc/toxiproxy"},
				},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: boolPtr(false),
					ReadOnlyRootFilesystem:   boolPtr(true),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("50m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
				},
			},
		},
		Volumes: []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: "toxiproxy-config"},
					},
				},
			},
		},
		SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: boolPtr(true)},
	}
	pt := buildDeployment(h.Namespace, "toxiproxy", pod)
	_, err := m.Client.AppsV1().Deployments(h.Namespace).Create(ctx, pt, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("harness: create toxiproxy deployment: %w", err)
	}
	return nil
}

func (m *K8sMaterializer) applySUTDeployment(ctx context.Context, h *Harness, service string) error {
	pod := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  service,
				Image: m.SUTImage,
				Env: []corev1.EnvVar{
					{Name: "ORION_RUN_ID", Value: h.RunID},
					{Name: "ORION_SERVICE", Value: service},
				},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: boolPtr(false),
					ReadOnlyRootFilesystem:   boolPtr(true),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(m.CPURequest),
						corev1.ResourceMemory: resource.MustParse(m.MemoryRequest),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(m.CPULimit),
						corev1.ResourceMemory: resource.MustParse(m.MemoryLimit),
					},
				},
			},
		},
		SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: boolPtr(true)},
	}
	pt := buildDeployment(h.Namespace, service, pod)
	_, err := m.Client.AppsV1().Deployments(h.Namespace).Create(ctx, pt, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("harness: create SUT deployment %q: %w", service, err)
	}
	return nil
}

func distinctServices(h *Harness) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range h.Workload.Endpoints {
		if e.Service == "" || seen[e.Service] {
			continue
		}
		seen[e.Service] = true
		out = append(out, e.Service)
	}
	return out
}

func boolPtr(b bool) *bool { return &b }
