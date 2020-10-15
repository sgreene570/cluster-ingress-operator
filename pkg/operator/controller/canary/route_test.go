package canary

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	routev1 "github.com/openshift/api/route/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestDesiredCanaryRoute(t *testing.T) {
	deploymentRef := metav1.OwnerReference{
		Name: "test",
	}
	service := desiredCanaryService(deploymentRef)
	route := desiredCanaryRoute(service)

	expectedRouteName := types.NamespacedName{
		Namespace: "openshift-ingress-canary",
		Name:      "ingress-canary-route",
	}

	if !cmp.Equal(route.Name, expectedRouteName.Name) {
		t.Errorf("Expected route name to be %s, but got %s", expectedRouteName.Name, route.Name)
	}

	if !cmp.Equal(route.Namespace, expectedRouteName.Namespace) {
		t.Errorf("Expected route namespace to be %s, but got %s", expectedRouteName.Namespace, route.Namespace)
	}

	expectedLabels := map[string]string{
		"ingress.openshift.io/canary": "canary_controller",
	}
	if !cmp.Equal(route.Labels, expectedLabels) {
		t.Errorf("Expected route labels to be %q, but got %q", expectedLabels, route.Labels)
	}

	routeToName := route.Spec.To.Name
	if !cmp.Equal(routeToName, service.Name) {
		t.Errorf("Expected route.Spec.To.Name to be %q, but got %q", service.Name, routeToName)
	}

	routeTarget := route.Spec.Port.TargetPort
	validTarget := false
	for _, port := range service.Spec.Ports {
		if cmp.Equal(routeTarget, port.TargetPort) {
			validTarget = true
		}
	}

	if !validTarget {
		t.Errorf("Expected route targetPort to be a port in the parameter service. Route targetPort not in service targetPort list")
	}
}

func TestCanaryRouteChanged(t *testing.T) {
	testCases := []struct {
		description string
		mutate      func(*routev1.Route)
		expect      bool
	}{
		{
			description: "if nothing changes",
			mutate:      func(_ *routev1.Route) {},
			expect:      false,
		},
		{
			description: "if route spec changes",
			mutate: func(route *routev1.Route) {
				route.Spec.To.Name = "test"
			},
			expect: true,
		},
	}

	deploymentRef := metav1.OwnerReference{
		Name: "test",
	}
	service := desiredCanaryService(deploymentRef)

	for _, tc := range testCases {
		original := desiredCanaryRoute(service)
		mutated := original.DeepCopy()
		tc.mutate(mutated)
		if changed, updated := canaryRouteChanged(original, mutated); changed != tc.expect {
			t.Errorf("%s, expect canaryRouteChanged to be %t, got %t", tc.description, tc.expect, changed)
		} else if changed {
			if changedAgain, _ := canaryRouteChanged(mutated, updated); changedAgain {
				t.Errorf("%s, canaryRouteChanged does not behave as a fixed point function", tc.description)
			}
		}
	}
}
