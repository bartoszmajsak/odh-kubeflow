/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	authorino "github.com/kuadrant/authorino/api/v1beta1"
	nbv1 "github.com/kubeflow/kubeflow/components/notebook-controller/api/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	maistrav1 "maistra.io/api/core/v1"
	"reflect"
	"regexp"
)

func (r *OpenshiftNotebookReconciler) ReconcileServiceMeshMembership(notebook *nbv1.Notebook, ctx context.Context) error {
	log := r.Log.WithValues("notebook", notebook.Name, "namespace", notebook.Namespace)

	desiredMember := r.createMeshMember(notebook)

	foundMember := &maistrav1.ServiceMeshMember{}
	justCreated := false
	err := r.Get(ctx, types.NamespacedName{
		Name:      desiredMember.Name,
		Namespace: notebook.Namespace,
	}, foundMember)
	if err != nil {
		if apierrs.IsNotFound(err) {
			log.Info("Adding namespace to the mesh")
			err = r.Create(ctx, desiredMember)
			if err != nil && !apierrs.IsAlreadyExists(err) {
				log.Error(err, "Unable to create ServiceMeshMember")
				return err
			}
			justCreated = true
		} else {
			log.Error(err, "Unable to fetch the ServiceMeshMember")
			return err
		}
	}

	// Reconcile the membership spec if it has been manually modified
	if !justCreated && !compareMeshMembers(*desiredMember, *foundMember) {
		log.Info("Reconciling ServiceMeshMember")
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := r.Get(ctx, types.NamespacedName{
				Name:      desiredMember.Name,
				Namespace: notebook.Namespace,
			}, foundMember); err != nil {
				return err
			}
			foundMember.Spec = desiredMember.Spec
			foundMember.ObjectMeta.Labels = desiredMember.ObjectMeta.Labels
			return r.Update(ctx, foundMember)
		})
		if err != nil {
			log.Error(err, "Unable to reconcile the ServiceMeshMember")
			return err
		}
	}

	return nil
}

func (r *OpenshiftNotebookReconciler) ReconcileAuthConfig(notebook *nbv1.Notebook, ctx context.Context) error {
	log := r.Log.WithValues("notebook", notebook.Name, "namespace", notebook.Namespace)

	desiredAuthConfig := r.createAuthConfig(notebook)

	foundAuthConfig := &authorino.AuthConfig{}
	justCreated := false
	err := r.Get(ctx, types.NamespacedName{
		Name:      desiredAuthConfig.Name,
		Namespace: notebook.Namespace,
	}, foundAuthConfig)
	if err != nil {
		if apierrs.IsNotFound(err) {
			log.Info("Creating Authorino AuthConfig")
			// Create the AuthConfig in the Openshift cluster
			err = r.Create(ctx, desiredAuthConfig)
			if err != nil && !apierrs.IsAlreadyExists(err) {
				log.Error(err, "Unable to create Authorino AuthConfig")
				return err
			}
			justCreated = true
		} else {
			log.Error(err, "Unable to fetch the AuthConfig")
			return err
		}
	}

	// Reconcile the Authorino AuthConfig if it has been manually modified
	if !justCreated && !compareAuthConfigs(*desiredAuthConfig, *foundAuthConfig) {
		log.Info("Reconciling Authorino AuthConfig")
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := r.Get(ctx, types.NamespacedName{
				Name:      desiredAuthConfig.Name,
				Namespace: notebook.Namespace,
			}, foundAuthConfig); err != nil {
				return err
			}
			foundAuthConfig.Spec = desiredAuthConfig.Spec
			foundAuthConfig.ObjectMeta.Labels = desiredAuthConfig.ObjectMeta.Labels
			return r.Update(ctx, foundAuthConfig)
		})
		if err != nil {
			log.Error(err, "Unable to reconcile the Authorino AuthConfig")
			return err
		}
	}

	return nil
}

func (r *OpenshiftNotebookReconciler) createAuthConfig(notebook *nbv1.Notebook) *authorino.AuthConfig {
	return &authorino.AuthConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AuthConfig",
			APIVersion: "authorino.kuadrant.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      notebook.Name + "-protection",
			Namespace: notebook.Namespace,
			Labels: map[string]string{
				"authorino/topic": "odh",
			},
		},
		Spec: authorino.AuthConfigSpec{
			Hosts: []string{
				removeProtocolPrefix(notebook.Annotations[AnnotationHubUrl]),
			},
			Identity: []*authorino.Identity{
				{
					Name: "authorized-service-accounts",
					KubernetesAuth: &authorino.Identity_KubernetesAuth{
						Audiences: []string{
							"https://kubernetes.default.svc",
						},
					},
				},
			},
			Authorization: []*authorino.Authorization{
				{
					Name: "k8s-rbac",
					KubernetesAuthz: &authorino.Authorization_KubernetesAuthz{
						User: authorino.StaticOrDynamicValue{
							ValueFrom: authorino.ValueFrom{
								AuthJSON: "auth.identity.username",
							},
						},
					},
				},
			},
			Response: []*authorino.Response{
				{
					Name: "x-auth-data",
					JSON: &authorino.Response_DynamicJSON{
						Properties: []authorino.JsonProperty{
							{
								Name: "username",
								ValueFrom: authorino.ValueFrom{
									AuthJSON: "auth.identity.username",
								},
							},
						},
					},
				},
			},
			DenyWith: &authorino.DenyWith{
				Unauthorized: &authorino.DenyWithSpec{
					Message: &authorino.StaticOrDynamicValue{
						Value: "Authorino Denied",
					},
				},
			},
		},
	}
}

func compareAuthConfigs(a1, a2 authorino.AuthConfig) bool {
	return reflect.DeepEqual(a1.ObjectMeta.Labels, a2.ObjectMeta.Labels) &&
		reflect.DeepEqual(a1.Spec, a2.Spec)
}

func compareMeshMembers(m1, m2 maistrav1.ServiceMeshMember) bool {
	return reflect.DeepEqual(m1.ObjectMeta.Labels, m2.ObjectMeta.Labels) &&
		reflect.DeepEqual(m1.Spec, m2.Spec)
}

func (r *OpenshiftNotebookReconciler) createMeshMember(notebook *nbv1.Notebook) *maistrav1.ServiceMeshMember {
	return &maistrav1.ServiceMeshMember{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: notebook.Namespace,
		},
		Spec: maistrav1.ServiceMeshMemberSpec{
			ControlPlaneRef: maistrav1.ServiceMeshControlPlaneRef{
				Name:      "basic",
				Namespace: "istio-system",
			},
		},
	}
}

func removeProtocolPrefix(s string) string {
	r := regexp.MustCompile(`^(https?://)`)
	return r.ReplaceAllString(s, "")
}
