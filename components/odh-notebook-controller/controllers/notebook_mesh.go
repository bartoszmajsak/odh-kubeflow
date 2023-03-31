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
	nbv1 "github.com/kubeflow/kubeflow/components/notebook-controller/api/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	maistrav1 "maistra.io/api/core/v1"
	"reflect"
)

func (r *OpenshiftNotebookReconciler) ReconcileServiceMeshMembership(notebook *nbv1.Notebook, ctx context.Context) error {
	// Initialize logger format
	log := r.Log.WithValues("notebook", notebook.Name, "namespace", notebook.Namespace)

	// Generate the desired service mesh member
	desiredMember := r.createMeshMember(notebook)

	// Create the route if it does not already exist
	foundMember := &maistrav1.ServiceMeshMember{}
	justCreated := false
	err := r.Get(ctx, types.NamespacedName{
		Name:      desiredMember.Name,
		Namespace: notebook.Namespace,
	}, foundMember)
	if err != nil {
		if apierrs.IsNotFound(err) {
			log.Info("Adding namespace to the mesh")
			// Create the SSMR in the Openshift cluster
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
		// Retry the update operation when the ingress controller eventually
		// updates the resource version field
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the last smm revision
			if err := r.Get(ctx, types.NamespacedName{
				Name:      desiredMember.Name,
				Namespace: notebook.Namespace,
			}, foundMember); err != nil {
				return err
			}
			// Reconcile labels and spec field
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

func compareMeshMembers(m1, m2 maistrav1.ServiceMeshMember) bool {
	// Two routes will be equal if the labels and spec are identical
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
