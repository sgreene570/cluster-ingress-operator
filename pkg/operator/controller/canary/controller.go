package canary

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	logf "github.com/openshift/cluster-ingress-operator/pkg/log"
	"github.com/openshift/cluster-ingress-operator/pkg/manifests"

	"github.com/google/go-cmp/cmp"
	"github.com/tcnksm/go-httpstat"

	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"

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

	// Start canary route polling loop
	reconciler.startCanaryRoutePolling(config.Stop)
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
	Stop        chan struct{}
}

// reconciler handles the actual canary reconciliation logic in response to
// events.
type reconciler struct {
	Config

	client client.Client
	cache  cache.Cache
}

func (r *reconciler) startCanaryRoutePolling(stop <-chan struct{}) error {
	//TODO
	// check ingress controller status before starting polling loop?
	count := 0
	go wait.Until(func() {
		haveRoute, route, err := r.currentCanaryRoute()
		if err != nil || !haveRoute {
			log.Error(err, "failed to get canary route")
			return
		}

		// Periodically rotate route endpoint every 5 minutes
		if count == 6 {
			haveService, service, err := r.currentCanaryService()
			if err != nil || !haveService {
				log.Error(err, "failed to get canary service")
				return
			}
			route, err = r.rotateRouteEndpoint(service, route)
			if err != nil {
				log.Error(err, "failed to rotate canary route endpoint")
				return
			}
			log.Info("Rotate route endpoint, now on", "port", route.Spec.Port.TargetPort.String())
			count = 0
			// Give router time to reload
			return
		}

		success, err := testRouteEndpoint(route)
		host := route.Spec.Host
		if !success {
			log.Error(err, "canary route check:")
			SetCanaryRouteUnreachable(host)
			return
		} else {
			log.Info("Successful canary check?")
			SetCanaryRouteReachable(host)
			count++
		}
	}, 1*time.Minute, stop)

	return nil
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

// createRequest forms a url by combining the prefix (ie http://) and
// host (ie <route>-<service>.apps.<cluster-domain>), and calls
// http.NewRequest with the created url.
func createRequest(host, prefix string) (*http.Request, error) {
	url := prefix + host
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// newHTTPClient creates an http Client with the specified
// timeout and the default transport.
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// newHTTPSClient creates an http Client with the specified
// timeout, and an http Transport created from config.
func newHTTPSClient(timeout time.Duration, config *tls.Config) *http.Client {
	client := newHTTPClient(timeout)
	client.Transport = utilnet.SetTransportDefaults(&http.Transport{TLSClientConfig: config, DisableKeepAlives: true})
	return client
}

// testRouteEndpoint probes the given route's host
// and returns a bool indicating whether or not the request was
// successful, along with an err if applicable.
func testRouteEndpoint(route *routev1.Route) (bool, error) {
	host := route.Spec.Host

	if len(host) == 0 {
		return false, fmt.Errorf("route.Spec.Host is nil, cannot test route")
	}

	// Create HTTP request
	request, err := createRequest(host, "http://")
	if err != nil {
		return false, fmt.Errorf("Error creating canary HTTP request: %v", err)
	}

	// Create HTTP result
	// for request stats tracking.
	result := &httpstat.Result{}

	// Get request context
	ctx := httpstat.WithHTTPStat(request.Context(), result)
	request = request.WithContext(ctx)

	// Send the HTTP request
	timeout, _ := time.ParseDuration("10s")
	client := newHTTPClient(timeout)
	response, err := client.Do(request)

	if err != nil {
		// Check if there were DNS errors
		dnsErr := &net.DNSError{}
		if errors.As(err, &dnsErr) {
			// Handle DNS error
			return false, fmt.Errorf("Error sending canary HTTP request: DNS error: %v", err)
		}
		return false, fmt.Errorf("Error sending canary HTTP request on host %s: %v", host, err)
	}

	// Read body and record current time
	bodyBytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return false, fmt.Errorf("Error reading canary response body: %v", err)
	}
	body := string(bodyBytes)
	response.Body.Close()
	t := time.Now()
	// Mark request as finished
	result.End(t)
	totalTime := result.Total(t)

	// log.Info("Total canary request time milliseconds", "time", totalTime.Milliseconds())

	/**
	// Example timing info
	dnsTime := result.DNSLookup.Milliseconds()
	tcpTime := result.TCPConnection.Milliseconds()
	tlsTime := result.TLSHandshake.Milliseconds()
	serverProcessingTime := result.ServerProcessing.Milliseconds()
	contentTransferTime := result.ContentTransfer(t)
	*/

	// Verify body contents
	if len(body) == 0 {
		return false, fmt.Errorf("Expected canary response body to not be nil")
	}

	expectedBodyContents := "Hello OpenShift!"
	if !strings.Contains(body, expectedBodyContents) {
		return false, fmt.Errorf("Expected canary request body to contain %s, instead got %s", expectedBodyContents, body)
	}

	// Verify that the request was received on the correct port
	recPort := response.Header.Get("request-port")
	if len(recPort) == 0 {
		return false, fmt.Errorf("Expected 'request-port' header in canary response to have a non-nil value")

	}
	routePortStr := route.Spec.Port.TargetPort.String()
	if !strings.Contains(routePortStr, recPort) {
		// router wedged, register in metrics counter
		CanaryEndpointWrongPortEcho.Inc()
		return false, fmt.Errorf("Canary request received on port %s, but route specifies %v", recPort, routePortStr)
	}

	// Check status code
	status := response.StatusCode
	switch status {
	case 200:
		// Register total time in metrics (use milliseconds)
		CanaryRequestTime.WithLabelValues(host).Observe(float64(totalTime.Milliseconds()))
	case 408:
		return false, fmt.Errorf("Status code %d: request timed out", status)
	case 503:
		return false, fmt.Errorf("Status code %d: Route not available via router", status)
	default:
		return false, fmt.Errorf("Unexpected status code: %d", status)
	}

	return true, nil
}
