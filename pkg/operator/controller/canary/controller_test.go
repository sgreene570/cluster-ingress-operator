package canary

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestChooseRandomServicePort(t *testing.T) {
	tPort1 := intstr.IntOrString{
		StrVal: "80",
	}
	tPort2 := intstr.IntOrString{
		StrVal: "8080",
	}
	tPort3 := intstr.IntOrString{
		StrVal: "8888",
	}
	testCases := []struct {
		description string
		route       *routev1.Route
		service     *corev1.Service
		success     bool
	}{
		{
			description: "service with no ports",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{
					Port: &routev1.RoutePort{},
				},
			},
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{},
				},
			},
			success: false,
		},
		{
			description: "service has one port",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{
					Port: &routev1.RoutePort{},
				},
			},
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							TargetPort: tPort1,
						},
					},
				},
			},
			success: false,
		},
		{
			description: "service has two ports",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{
					Port: &routev1.RoutePort{
						TargetPort: tPort1,
					},
				},
			},
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							TargetPort: tPort2,
						},
						{
							TargetPort: tPort3,
						},
					},
				},
			},
			success: true,
		},
	}

	for _, tc := range testCases {
		route, err := chooseRandomServicePort(tc.service, tc.route)
		if tc.success {
			if err != nil {
				t.Errorf("Expected test case %s to not return an err, but got err %v", tc.description, err)
			}

			if cmp.Equal(tc.route.Spec.Port, route.Spec.Port) {
				t.Errorf("Expected route to have a new port assignment. Still has port %s", route.Spec.Port)
			}
		} else {
			if err == nil {
				t.Errorf("Expected test case %s to return an err, but it did not", tc.description)
			}
		}
	}
}
