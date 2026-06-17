package sandbox

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// KubernetesNamespaceProvisioner is the production NamespaceProvisioner
// backed by k8s.io/client-go. Idempotent on Name per SPEC §10.3:
// AlreadyExists translates to Created=false.
type KubernetesNamespaceProvisioner struct {
	Client kubernetes.Interface
}

// NewKubernetesNamespaceProvisioner builds a provisioner.
func NewKubernetesNamespaceProvisioner(client kubernetes.Interface) *KubernetesNamespaceProvisioner {
	return &KubernetesNamespaceProvisioner{Client: client}
}

// Provision creates the namespace + ResourceQuota per SPEC §10.3. The
// PodSecurity restricted enforcement is applied via namespace labels.
func (p *KubernetesNamespaceProvisioner) Provision(ctx context.Context, spec NamespaceSpec) (ProvisionResult, error) {
	if p.Client == nil {
		return ProvisionResult{}, errors.New("sandbox: nil kubernetes.Interface")
	}
	if spec.Name == "" {
		return ProvisionResult{}, errors.New("sandbox: NamespaceSpec.Name required")
	}
	if spec.PodSecurity == "" {
		spec.PodSecurity = "restricted"
	}
	labels := map[string]string{
		"app.kubernetes.io/name":             "orion-worker",
		"orion.revelara.ai/run-id":           spec.RunID.String(),
		"orion.revelara.ai/tenant-id":        spec.TenantID.String(),
		"pod-security.kubernetes.io/enforce": spec.PodSecurity,
		"pod-security.kubernetes.io/audit":   spec.PodSecurity,
		"pod-security.kubernetes.io/warn":    spec.PodSecurity,
	}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Labels: labels},
	}
	created := true
	got, err := p.Client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			created = false
			got, err = p.Client.CoreV1().Namespaces().Get(ctx, spec.Name, metav1.GetOptions{})
			if err != nil {
				return ProvisionResult{}, fmt.Errorf("sandbox: get existing namespace: %w", err)
			}
		} else {
			return ProvisionResult{}, fmt.Errorf("sandbox: create namespace: %w", err)
		}
	}
	if spec.CPULimit != "" && spec.MemoryLimit != "" {
		if err := p.applyResourceQuota(ctx, spec); err != nil {
			return ProvisionResult{}, err
		}
	}
	return ProvisionResult{
		Name:    got.Name,
		Created: created,
		UID:     string(got.UID),
	}, nil
}

// Delete removes the namespace. NotFound is treated as success.
func (p *KubernetesNamespaceProvisioner) Delete(ctx context.Context, name string) error {
	if p.Client == nil {
		return errors.New("sandbox: nil kubernetes.Interface")
	}
	if err := p.Client.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("sandbox: delete namespace: %w", err)
	}
	return nil
}

func (p *KubernetesNamespaceProvisioner) applyResourceQuota(ctx context.Context, spec NamespaceSpec) error {
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orion-worker-quota",
			Namespace: spec.Name,
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceLimitsCPU:    resource.MustParse(spec.CPULimit),
				corev1.ResourceLimitsMemory: resource.MustParse(spec.MemoryLimit),
			},
		},
	}
	_, err := p.Client.CoreV1().ResourceQuotas(spec.Name).Create(ctx, quota, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("sandbox: create resource quota: %w", err)
	}
	return nil
}

// KubernetesNetworkPolicyApplier is the production NetworkPolicyApplier
// backed by k8s.io/client-go. Applies the default-deny + narrow-allow
// rules per SPEC §10.3.
type KubernetesNetworkPolicyApplier struct {
	Client kubernetes.Interface
}

// NewKubernetesNetworkPolicyApplier builds the applier.
func NewKubernetesNetworkPolicyApplier(client kubernetes.Interface) *KubernetesNetworkPolicyApplier {
	return &KubernetesNetworkPolicyApplier{Client: client}
}

// Apply installs the default-deny + selective-allow policy pair into
// the namespace. Idempotent: AlreadyExists is treated as success.
func (a *KubernetesNetworkPolicyApplier) Apply(ctx context.Context, spec NetworkPolicySpec) error {
	if a.Client == nil {
		return errors.New("sandbox: nil kubernetes.Interface")
	}
	if spec.Namespace == "" {
		return errors.New("sandbox: NetworkPolicySpec.Namespace required")
	}
	deny := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orion-worker-default-deny",
			Namespace: spec.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
		},
	}
	if err := a.createOrIgnoreExists(ctx, spec.Namespace, deny); err != nil {
		return err
	}

	egress := buildEgressAllowPolicy(spec)
	return a.createOrIgnoreExists(ctx, spec.Namespace, egress)
}

// Remove deletes the policies created by Apply. NotFound is success.
func (a *KubernetesNetworkPolicyApplier) Remove(ctx context.Context, namespace string) error {
	if a.Client == nil {
		return errors.New("sandbox: nil kubernetes.Interface")
	}
	for _, name := range []string{"orion-worker-default-deny", "orion-worker-egress-allow"} {
		if err := a.Client.NetworkingV1().NetworkPolicies(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("sandbox: delete network policy %s: %w", name, err)
			}
		}
	}
	return nil
}

func (a *KubernetesNetworkPolicyApplier) createOrIgnoreExists(ctx context.Context, namespace string, policy *networkingv1.NetworkPolicy) error {
	_, err := a.Client.NetworkingV1().NetworkPolicies(namespace).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("sandbox: create network policy %s: %w", policy.Name, err)
	}
	return nil
}

func buildEgressAllowPolicy(spec NetworkPolicySpec) *networkingv1.NetworkPolicy {
	var egress []networkingv1.NetworkPolicyEgressRule

	// DNS to kube-system.
	egress = append(egress, networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{
			{NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
			}},
		},
		Ports: []networkingv1.NetworkPolicyPort{
			{Port: intOrString(53), Protocol: protocolPtr(corev1.ProtocolUDP)},
			{Port: intOrString(53), Protocol: protocolPtr(corev1.ProtocolTCP)},
		},
	})

	// CIDR allowlist for LLM provider + Postgres + control plane.
	cidrPorts := spec.AllowedEgressPorts
	if len(cidrPorts) == 0 {
		cidrPorts = []int32{443, 5432}
	}
	for _, cidr := range spec.AllowedEgressCIDRs {
		ports := make([]networkingv1.NetworkPolicyPort, 0, len(cidrPorts))
		for _, p := range cidrPorts {
			ports = append(ports, networkingv1.NetworkPolicyPort{
				Port:     intOrString(p),
				Protocol: protocolPtr(corev1.ProtocolTCP),
			})
		}
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{
			To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: cidr}}},
			Ports: ports,
		})
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orion-worker-egress-allow",
			Namespace: spec.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      egress,
		},
	}
}

func intOrString(v int32) *intstr.IntOrString {
	x := intstr.FromInt32(v)
	return &x
}

func protocolPtr(p corev1.Protocol) *corev1.Protocol { return &p }
