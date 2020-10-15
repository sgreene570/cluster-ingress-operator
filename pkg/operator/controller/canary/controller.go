package canary

import (
	"fmt"
	"math/rand"

	logf "github.com/openshift/cluster-ingress-operator/pkg/log"
	"github.com/openshift/cluster-ingress-operator/pkg/manifests"

	"github.com/google/go-cmp/cmp"

	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	controllerName = "canary_controller"
)

var (
	log = logf.Logger.WithName(controllerName)
)

// New creates the ingress canary controller.
//
// The canary controller will watch the Default IngressController, as well as
// the canary service, deployment, and route resources.
func New(mgr manager.Manager, config Config) (controller.Controller, error) {
	reconciler := &reconciler{
		Config: config,
		client: mgr.GetClient(),
		cache:  mgr.GetCache(),
	}
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: reconciler})
	if err != nil {
		return nil, err
	}

	if err := c.Watch(&source.Kind{Type: &operatorv1.IngressController{}}, enqueueRequestForDefaultIngressController(config.Namespace)); err != nil {
		return nil, err
	}
	if err := c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, enqueueRequestForIngressCanary(manifests.DefaultCanaryNamespace)); err != nil {
		return nil, err
	}
	if err := c.Watch(&source.Kind{Type: &corev1.Service{}}, enqueueRequestForIngressCanary(manifests.DefaultCanaryNamespace)); err != nil {
		return nil, err
	}
	if err := c.Watch(&source.Kind{Type: &routev1.Route{}}, enqueueRequestForIngressCanary(manifests.DefaultCanaryNamespace)); err != nil {
		return nil, err
	}
	return c, nil
}

// Reconcile ensures that the ingress canary controller's resources
// are in the desired state.
func (r *reconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	result := reconcile.Result{}
	errors := []error{}

	if err := r.ensureCanaryNamespace(); err != nil {
		errors = append(errors, err)
		return result, utilerrors.NewAggregate(errors)
	}

	haveDepl, deployment, err := r.ensureCanaryDeployment()
	if err != nil {
		errors = append(errors, err)
		return result, utilerrors.NewAggregate(errors)
	}
	if !haveDepl {
		errors = append(errors, fmt.Errorf("failed to get canary deployment"))
		return result, utilerrors.NewAggregate(errors)
	}

	trueVar := true
	deploymentRef := metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       deployment.Name,
		UID:        deployment.UID,
		Controller: &trueVar,
	}

	haveService, service, err := r.ensureCanaryService(deploymentRef)
	if err != nil {
		errors = append(errors, err)
		return result, utilerrors.NewAggregate(errors)
	}
	if !haveService {
		errors = append(errors, fmt.Errorf("failed to get canary service"))
		return result, utilerrors.NewAggregate(errors)
	}

	if haveRoute, _, err := r.ensureCanaryRoute(service); err != nil {
		if err != nil {
			errors = append(errors, err)
			return result, utilerrors.NewAggregate(errors)
		}
		if !haveRoute {
			errors = append(errors, fmt.Errorf("failed to get canary route"))
			return result, utilerrors.NewAggregate(errors)
		}
	}

	return result, nil
}

// enqueueRequestForIngressCanary returns reconcile requests for
// properly labeled ingress canary resources in the given namespace.
func enqueueRequestForIngressCanary(namespace string) handler.EventHandler {
	return &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(a handler.MapObject) []reconcile.Request {
			labels := a.Meta.GetLabels()
			if _, ok := labels[manifests.OwningIngressCanaryCheckLabel]; ok {
				log.Info("queueing ingress canary", "related", a.Meta.GetSelfLink())
				return []reconcile.Request{
					{
						NamespacedName: types.NamespacedName{
							Namespace: namespace,
							Name:      a.Meta.GetName(),
						},
					},
				}
			} else {
				return []reconcile.Request{}
			}
		}),
	}
}

// enqueueRequestForDefaultIngressController returns canary controller
// reconcile requests for the default ingress controller.
func enqueueRequestForDefaultIngressController(namespace string) handler.EventHandler {
	return &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(a handler.MapObject) []reconcile.Request {
			if cmp.Equal(a.Meta.GetName(), manifests.DefaultIngressControllerName) {
				log.Info("queueing ingress canary", "related", a.Meta.GetSelfLink())
				return []reconcile.Request{
					{
						NamespacedName: types.NamespacedName{
							Namespace: namespace,
							Name:      manifests.DefaultIngressControllerName,
						},
					},
				}
			} else {
				return []reconcile.Request{}
			}
		}),
	}
}

// Config holds all the things necessary for the controller to run.
type Config struct {
	Namespace   string
	CanaryImage string
}

// reconciler handles the actual canary reconciliation logic in response to
// events.
type reconciler struct {
	Config

	client client.Client
	cache  cache.Cache
}

// Switch the current RoutePort that the route points to.
// Use this function to periodically update the canary route endpoint
// to verify if the router has wedged.
func (r *reconciler) rotateRouteEndpoint(service *corev1.Service, current *routev1.Route) (*routev1.Route, error) {

	updated, err := chooseRandomServicePort(service, current)
	if err != nil {
		return nil, fmt.Errorf("Failed to rotate route port: %v", err)
	}

	// update route resource here
	_, err = r.updateCanaryRoute(current, updated)
	if err != nil {
		return current, err
	}

	return updated, nil
}

// chooseRandomService returns a route resource with Spec.Port set to a random
// port selected from service.Spec.Ports, exlcuding route.Spec.Port to gaurantee
// that a new port is selected.
func chooseRandomServicePort(service *corev1.Service, route *routev1.Route) (*routev1.Route, error) {
	servicePorts := service.Spec.Ports
	currentPort := route.Spec.Port

	numServicePorts := len(servicePorts)
	if numServicePorts == 0 {
		return nil, fmt.Errorf("Service has no ports")
	} else if numServicePorts == 1 {
		return nil, fmt.Errorf("Service has only one port, no change possible")
	}

	updated := route.DeepCopy()

	availablePorts := []intstr.IntOrString{}
	// Filter currentPort from servicePorts
	for _, port := range servicePorts {
		if !cmp.Equal(port.TargetPort, currentPort.TargetPort) {
			availablePorts = append(availablePorts, port.TargetPort)
		}
	}

	updated.Spec.Port = &routev1.RoutePort{
		TargetPort: availablePorts[rand.Intn(len(availablePorts))],
	}

	return updated, nil
}
