package canary

import (
	"context"
	"fmt"

	"github.com/openshift/cluster-ingress-operator/pkg/manifests"
	"github.com/openshift/cluster-ingress-operator/pkg/operator/controller"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

// ensureCanaryRoute ensures the ingress canary route exists
func (r *reconciler) ensureCanaryRoute(service *corev1.Service) (bool, *routev1.Route, error) {
	desired := desiredCanaryRoute(service)
	haveRoute, current, err := r.currentCanaryRoute()
	if err != nil {
		return false, nil, err
	}

	if haveRoute {
		return true, current, nil
	} else {
		err := r.createCanaryRoute(desired)
		if err != nil {
			return false, nil, err
		}
	}
	return true, desired, nil
}

// currentCanaryRoute gets the current ingress canary route resource
func (r *reconciler) currentCanaryRoute() (bool, *routev1.Route, error) {
	route := &routev1.Route{}
	if err := r.client.Get(context.TODO(), controller.CanaryRouteName(), route); err != nil {
		if errors.IsNotFound(err) {
			return false, nil, nil
		}
		return false, nil, err
	}
	return true, route, nil
}

// createCanaryRoute creates the given route
func (r *reconciler) createCanaryRoute(route *routev1.Route) error {
	if err := r.client.Create(context.TODO(), route); err != nil {
		return fmt.Errorf("failed to create canary route %s/%s: %v", route.Namespace, route.Name, err)
	}

	log.Info("created canary route", "namespace", route.Namespace, "name", route.Name)
	return nil
}

// updateCanaryRoute updates the canary route if an appropriate change
// has been detected
func (r *reconciler) updateCanaryRoute(current, desired *routev1.Route) (bool, error) {
	changed, updated := canaryRouteChanged(current, desired)
	if !changed {
		return false, nil
	}

	if err := r.client.Update(context.TODO(), updated); err != nil {
		return false, fmt.Errorf("failed to update canary route %s/%s: %v", updated.Namespace, updated.Name, err)
	}
	log.Info("updated canary route", "namespace", updated.Namespace, "name", updated.Name)
	return true, nil
}

// deleteCanaryRoute deletes a given route
func (r *reconciler) deleteCanaryRoute(route *routev1.Route) (bool, error) {

	if err := r.client.Delete(context.TODO(), route); err != nil {
		return false, fmt.Errorf("failed to delete canary route %s/%s: %v", route.Namespace, route.Name, err)
	}

	log.Info("deleted canary route", "namespace", route.Namespace, "name", route.Name)
	return true, nil
}

// canaryRouteChanged returns true if current and expected differ in Spec
func canaryRouteChanged(current, expected *routev1.Route) (bool, *routev1.Route) {
	changed := false
	updated := current.DeepCopy()

	if !cmp.Equal(current.Spec, expected.Spec, cmpopts.EquateEmpty()) {
		updated.Spec = expected.Spec
		changed = true
	}

	if !changed {
		return false, nil
	}
	return true, updated
}

// desiredCanaryRoute returns the desired canary route read in
// from manifests
func desiredCanaryRoute(service *corev1.Service) *routev1.Route {
	route := manifests.CanaryRoute()

	name := controller.CanaryRouteName()

	route.Namespace = name.Namespace
	route.Name = name.Name

	route.Labels = map[string]string{
		// associate the route with the ingress canary controller
		manifests.OwningIngressCanaryCheckLabel: controllerName,
	}

	route.Spec.To.Name = controller.CanaryServiceName().Name

	// Set spec.port.targetPort to the first port available in the canary service.
	// The canary controller will toggle which targetPort the router targets
	// to test > 1 endpoint, so it does not matter which port is selected as long
	// as the canary service has > 1 ports available.
	route.Spec.Port.TargetPort = service.Spec.Ports[0].TargetPort

	return route
}
