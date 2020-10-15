package canary

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestDesiredCanaryService(t *testing.T) {
	deploymentRef := metav1.OwnerReference{
		Name: "test",
	}
	service := desiredCanaryService(deploymentRef)

	expectedServiceName := types.NamespacedName{
		Namespace: "openshift-ingress-canary",
		Name:      "ingress-canary",
	}

	if !cmp.Equal(service.Name, expectedServiceName.Name) {
		t.Errorf("Expected service name to be %s, but got %s", expectedServiceName.Name, service.Name)
	}

	if !cmp.Equal(service.Namespace, expectedServiceName.Namespace) {
		t.Errorf("Expected service namespace to be %s, but got %s", expectedServiceName.Namespace, service.Namespace)
	}

	expectedLabels := map[string]string{
		"ingress.openshift.io/canary": "canary_controller",
	}
	if !cmp.Equal(service.Labels, expectedLabels) {
		t.Errorf("Expected service labels to be %q, but got %q", expectedLabels, service.Labels)
	}

	expectedSelector := map[string]string{
		"app": "ingress-canary",
	}

	if !cmp.Equal(service.Spec.Selector, expectedSelector) {
		t.Errorf("Expected service selector to be %q, but got %q", expectedSelector, service.Spec.Selector)
	}

	ownerRefs := service.OwnerReferences

	if len(ownerRefs) != 1 {
		t.Errorf("Expected service owner references to be of length 1, got %d", len(ownerRefs))
	}

	if !cmp.Equal(ownerRefs[0], deploymentRef) {
		t.Errorf("Expected service owner reference name %q, but got %q", deploymentRef.Name, ownerRefs[0].Name)
	}

}
