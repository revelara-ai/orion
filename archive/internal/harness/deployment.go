package harness

// buildDeployment is a thin helper that wraps a PodSpec in a
// minimal Deployment with a single-pod replicaset. The K8sMaterializer
// uses it for both the SUT and the toxiproxy sidecar; the inputs
// differ but the wrapper is identical.

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func buildDeployment(namespace, name string, pod corev1.PodSpec) *appsv1.Deployment {
	labels := map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "orion-harness",
		"orion.revelara.ai/component":  name,
	}
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       pod,
			},
		},
	}
}
