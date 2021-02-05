// Copyright © 2021 Banzai Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nodeagent

import (
	"fmt"

	"emperror.dev/errors"
	"github.com/banzaicloud/logging-operator/pkg/resources"
	"github.com/banzaicloud/logging-operator/pkg/sdk/api/v1beta1"
	"github.com/banzaicloud/operator-tools/pkg/reconciler"
	"github.com/banzaicloud/operator-tools/pkg/typeoverride"
	util "github.com/banzaicloud/operator-tools/pkg/utils"
	"github.com/go-logr/logr"
	"github.com/imdario/mergo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	defaultServiceAccountName      = "fluentbit"
	clusterRoleBindingName         = "fluentbit"
	clusterRoleName                = "fluentbit"
	fluentBitSecretConfigName      = "fluentbit"
	fluentbitDaemonSetName         = "fluentbit"
	fluentbitPodSecurityPolicyName = "fluentbit"
	fluentbitServiceName           = "fluentbit"
	containerName                  = "fluent-bit"
)

func NodeAgentFluentbitDefaults() (n *v1beta1.NodeAgent) {
	n = &v1beta1.NodeAgent{
		FluentbitSpec: &v1beta1.NodeAgentFluentbit{
			DaemonSetOverrides: &typeoverride.DaemonSet{
				Spec: typeoverride.DaemonSetSpec{
					Template: typeoverride.PodTemplateSpec{
						Spec: typeoverride.PodSpec{
							Containers: []v1.Container{
								{
									Name:            containerName,
									Image:           "fluent/fluent-bit:1.6.8",
									ImagePullPolicy: v1.PullIfNotPresent,
									Resources: v1.ResourceRequirements{
										Limits: v1.ResourceList{
											v1.ResourceMemory: resource.MustParse("100M"),
											v1.ResourceCPU:    resource.MustParse("200m"),
										},
										Requests: v1.ResourceList{
											v1.ResourceMemory: resource.MustParse("50M"),
											v1.ResourceCPU:    resource.MustParse("100m"),
										},
									},
									LivenessProbe: &v1.Probe{
										Handler: v1.Handler{
											HTTPGet: &v1.HTTPGetAction{
												Path: "/api/v1/metrics/prometheus",
												Port: intstr.IntOrString{
													IntVal: 2020,
												},
											}},
										InitialDelaySeconds: 10,
										TimeoutSeconds:      0,
										PeriodSeconds:       10,
										SuccessThreshold:    0,
										FailureThreshold:    3,
									},
								},
							},
						},
					},
				},
			},
			Flush:         1,
			Grace:         5,
			LogLevel:      "info",
			CoroStackSize: 24576,
			InputTail: v1beta1.InputTail{
				Path:            "/var/log/containers/*.log",
				RefreshInterval: "5",
				SkipLongLines:   "On",
				DB:              util.StringPointer("/tail-db/tail-containers-state.db"),
				MemBufLimit:     "5MB",
				Tag:             "kubernetes.*",
			},
			Security: &v1beta1.Security{
				RoleBasedAccessControlCreate: util.BoolPointer(true),
				SecurityContext:              &v1.SecurityContext{},
				PodSecurityContext:           &v1.PodSecurityContext{},
			},
			MountPath: "/var/lib/docker/containers",
			BufferStorage: v1beta1.BufferStorage{
				StoragePath: "/buffers",
			},
			FilterAws: &v1beta1.FilterAws{
				ImdsVersion:     "v2",
				AZ:              util.BoolPointer(true),
				Ec2InstanceID:   util.BoolPointer(true),
				Ec2InstanceType: util.BoolPointer(false),
				PrivateIP:       util.BoolPointer(false),
				AmiID:           util.BoolPointer(false),
				AccountID:       util.BoolPointer(false),
				Hostname:        util.BoolPointer(false),
				VpcID:           util.BoolPointer(false),
				Match:           "*",
			},
			ForwardOptions: &v1beta1.ForwardOptions{
				RetryLimit: "False",
			},
		},
	}

	return n
}

var NodeAgentFluentbitWindowsDefaults = &v1beta1.NodeAgent{
	FluentbitSpec: &v1beta1.NodeAgentFluentbit{
		MountPath: "C:\\ProgramData\\docker",
		DaemonSetOverrides: &typeoverride.DaemonSet{
			Spec: typeoverride.DaemonSetSpec{
				Template: typeoverride.PodTemplateSpec{
					Spec: typeoverride.PodSpec{
						NodeSelector: map[string]string{
							"kubernetes.io/os": "windows",
						},
						Tolerations: []v1.Toleration{{
							Key:      "os",
							Operator: "Equal",
							Value:    "windows",
							Effect:   "NoSchedule",
						},
						},
					}},
			}},

		Flush: 2},
}
var NodeAgentFluentbitLinuxDefaults = &v1beta1.NodeAgent{
	FluentbitSpec: &v1beta1.NodeAgentFluentbit{
		Flush: 3},
}

func generateLoggingRefLabels(loggingRef string) map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": loggingRef}
}

func (n *nodeAgentInstance) getFluentBitLabels() map[string]string {
	return util.MergeLabels(n.nodeAgent.Metadata.Labels, map[string]string{
		"app.kubernetes.io/name":     "fluentbit",
		"app.kubernetes.io/instance": n.nodeAgent.Name,
	}, generateLoggingRefLabels(n.logging.ObjectMeta.GetName()))
}

func (n *nodeAgentInstance) getServiceAccount() string {
	if n.nodeAgent.FluentbitSpec.Security.ServiceAccount != "" {
		return n.nodeAgent.FluentbitSpec.Security.ServiceAccount
	}
	return n.QualifiedName(defaultServiceAccountName)
}

//
//type DesiredObject struct {
//	Object runtime.Object
//	State  reconciler.DesiredState
//}
//
// Reconciler holds info what resource to reconcile
type Reconciler struct {
	Logging *v1beta1.Logging
	*reconciler.GenericResourceReconciler
	configs map[string][]byte
}

// NewReconciler creates a new NodeAgent reconciler
func New(client client.Client, logger logr.Logger, logging *v1beta1.Logging, opts reconciler.ReconcilerOpts) *Reconciler {
	return &Reconciler{
		Logging:                   logging,
		GenericResourceReconciler: reconciler.NewGenericReconciler(client, logger, opts),
	}
}

type nodeAgentInstance struct {
	nodeAgent  *v1beta1.NodeAgent
	reconciler *reconciler.GenericResourceReconciler
	logging    *v1beta1.Logging
	configs    map[string][]byte
}

// Reconcile reconciles the NodeAgent resource
func (r *Reconciler) Reconcile() (*reconcile.Result, error) {
	for _, a := range r.Logging.Spec.NodeAgents {
		var instance nodeAgentInstance
		err := mergo.Merge(a, NodeAgentFluentbitDefaults())
		if err != nil {
			return nil, err
		}

		switch a.Type {
		case "windows":
			err := mergo.Merge(a, NodeAgentFluentbitWindowsDefaults)
			if err != nil {
				return nil, err
			}
			instance = nodeAgentInstance{
				nodeAgent:  a,
				reconciler: r.GenericResourceReconciler,
				logging:    r.Logging,
			}
		default:
			err := mergo.Merge(a, NodeAgentFluentbitLinuxDefaults)
			if err != nil {
				return nil, err
			}
			instance = nodeAgentInstance{
				nodeAgent:  a,
				reconciler: r.GenericResourceReconciler,
				logging:    r.Logging,
			}

		}

		result, err := instance.Reconcile()
		if err != nil {
			return nil, errors.WrapWithDetails(err,
				"failed to reconcile instances", "NodeName", a.Name)
		}
		if result != nil {
			return result, nil
		}
	}
	return nil, nil
}

// Reconcile reconciles the nodeAgentInstance resource
func (n *nodeAgentInstance) Reconcile() (*reconcile.Result, error) {
	for _, factory := range []resources.Resource{
		n.serviceAccount,
		n.clusterRole,
		n.clusterRoleBinding,
		n.clusterPodSecurityPolicy,
		n.pspClusterRole,
		n.pspClusterRoleBinding,
		n.configSecret,
		n.daemonSet,
		n.serviceMetrics,
		n.monitorServiceMetrics,
	} {
		o, state, err := factory()
		if err != nil {
			return nil, errors.WrapIf(err, "failed to create desired object")
		}
		if o == nil {
			return nil, errors.Errorf("Reconcile error! Resource %#v returns with nil object", factory)
		}
		result, err := n.reconciler.ReconcileResource(o, state)
		if err != nil {
			return nil, errors.WrapWithDetails(err,
				"failed to reconcile resource", "resource", o.GetObjectKind().GroupVersionKind())
		}
		if result != nil {
			return result, nil
		}
	}

	return nil, nil
}

// nodeAgent QualifiedName
func (n *nodeAgentInstance) QualifiedName(name string) string {
	return fmt.Sprintf("%s-%s-%s", n.logging.Name, n.nodeAgent.Name, name)
}

//
//func RegisterWatches(builder *builder.Builder) *builder.Builder {
//	return builder.
//		Owns(&corev1.ConfigMap{}).
//		Owns(&appsv1.DaemonSet{}).
//		Owns(&rbacv1.ClusterRole{}).
//		Owns(&rbacv1.ClusterRoleBinding{}).
//		Owns(&corev1.ServiceAccount{})
//}
