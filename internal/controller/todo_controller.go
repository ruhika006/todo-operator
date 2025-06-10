/*
Copyright 2025.

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

package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	v1 "github.com/crossplane/crossplane-tools/api/v1"
	taskv1alpha1 "github.com/crossplane/crossplane-tools/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// TodoReconciler reconciles a todos object
type TodoReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Todos  []taskv1alpha1.Todo
}

// +kubebuilder:rbac:groups=task.example.com,resources=todoes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=task.example.com,resources=todoes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=task.example.com,resources=todoes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the todos object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile


const finalizer = "todo.task.example.com"

const CONDITION_STATUS_TRUE = metav1.ConditionTrue
const CONDITION_STATUS_FALSE = metav1.ConditionFalse

func updateCondition(conds *[]metav1.Condition, newCond metav1.Condition) {
	for i, cond := range *conds {
		if cond.Type == newCond.Type {
			(*conds)[i] = newCond
			return
		}
	}
	*conds = append(*conds, newCond)
}

func (r *TodoReconciler) AppendCondition(ctx context.Context, todo *taskv1alpha1.Todo, typeName string, status metav1.ConditionStatus, reason string, message string) error {
	log := log.FromContext(ctx)
	now := metav1.Now()

	condition := metav1.Condition{
		Type:               typeName,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	}

	updateCondition(&todo.Status.Condition, condition)

	if err := r.Status().Update(ctx, todo); err != nil {
		log.Error(err, "Failed to update status conditions on Todo")
		return err
	}
	return nil
}

func (r *TodoReconciler) CheckDelete(ctx context.Context, todo *taskv1alpha1.Todo, newTodo *v1.Todo) (ctrl.Result, error) {
	l := logf.FromContext(ctx)

	if !todo.ObjectMeta.DeletionTimestamp.IsZero() {
		if ctrlutil.ContainsFinalizer(todo, finalizer) {
			fmt.Println("Cleaning up Todo:", todo.Name)

			// API Delete TODO
			if newTodo != nil {
				if err := v1.DeleteTodo(newTodo.ID); err != nil {
					l.Error(err, "Failed to delete Todo", "id", todo.Status.ID)
					return ctrl.Result{}, err
				}
			}

			// Refresh before removing finalizer
			latest := &taskv1alpha1.Todo{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(todo), latest); err != nil {
				return ctrl.Result{}, err
			}
			ctrlutil.RemoveFinalizer(latest, finalizer)

			// Update the CR without finalizers, so that it is deleted. 
			if err := r.Update(ctx, latest); err != nil {
				return ctrl.Result{}, err
			}
			l.Info("Removed Finalizer -- Ready to be deleted by K8s")
		}
	}
	return ctrl.Result{}, nil
}

func (r *TodoReconciler) CreateTodo(ctx context.Context, todo *taskv1alpha1.Todo, newTodo *v1.Todo) (*v1.Todo, ctrl.Result, error) {
	l := logf.FromContext(ctx)

	// Only Create if newTodo (API todo) is not present,
	// but Todo CR is present. 

	if newTodo == nil && todo.TypeMeta.Kind!=""{
		var err error
		newTodo, err = v1.AddTodo(todo.Spec.Title)
		if err != nil {
			l.Error(err, "Failed to add a Todo via API")
			_ = r.AppendCondition(ctx, todo, "TodoAPI", CONDITION_STATUS_FALSE, "UpdateAPI", "Failed to add a new TODO object in the API")
			return nil, ctrl.Result{}, err
		}
		l.Info("Added a new Todo via API")
		_ = r.AppendCondition(ctx, todo, "TodoAPI", CONDITION_STATUS_TRUE, "UpdateAPI", "A new TODO object added in the API")
	}
	return newTodo, ctrl.Result{}, nil
}

func (r *TodoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := logf.FromContext(ctx)
	l.Info("Reconcile Started", "req", req)

	// Case 1: get the Custom Resource
	todo := &taskv1alpha1.Todo{}
	if err := r.Get(ctx, req.NamespacedName, todo); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Case 2: Ensure finalizer is set
	if !ctrlutil.ContainsFinalizer(todo, finalizer) {
		ctrlutil.AddFinalizer(todo, finalizer)
		if err := r.Update(ctx, todo); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Case 3: Match existing external Todo by title
	apiTodos, err := v1.ListTodos()
	if err != nil {
		l.Error(err, "Failed to list API Todos")
		return ctrl.Result{}, err
	}

	var newTodo *v1.Todo
	for _, apiTodo := range apiTodos {
		// Always compare with Spec. 
		if apiTodo.Title == todo.Spec.Title {
			newTodo = &apiTodo
			break
		}
	}

	// Case 4: Handle deletion
	if result, err := r.CheckDelete(ctx, todo, newTodo); err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	// Case 5: Create Todo in external API if not exists
	// Fetch latest Todo object in case it was deleted previously.
	if err := r.Get(ctx, req.NamespacedName, todo); err != nil {
		return ctrl.Result{}, err
	}

	newTodo, result, err := r.CreateTodo(ctx, todo, newTodo)
	if err != nil {
		l.Error(err, "Failed to create Todo via API")
		return result, err
	}


	// Case 6: Update the CR object's Status with the ID 
	// generated by the Todo Add/Create endpoint.
	todo.Status.ID = newTodo.ID
	if err := r.Status().Update(ctx, todo); err != nil {
		_ = r.AppendCondition(ctx, todo, "K8sAPI", CONDITION_STATUS_FALSE, "UpdateCR", "Could not update CR with Status ID")
		return ctrl.Result{}, err
	}
	_ = r.AppendCondition(ctx, todo, "K8sAPI", CONDITION_STATUS_TRUE, "UpdateCR", "Updated CR with Status ID")

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TodoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&taskv1alpha1.Todo{}).
		Named("todo").
		Complete(r)
}