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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
const CONDITION_STATUS_UNKNOWN = metav1.ConditionUnknown

// Upate the existing condition in Status, 
// only if it matches the type of the Incoming Condition, 
// rather than appending the new one directly. 
func updateCondition(conds *[]metav1.Condition, newCond metav1.Condition) []metav1.Condition {
	for i, cond := range *conds {
		if cond.Type == newCond.Type {
			(*conds)[i] = newCond
			return *conds
		}
	}
	*conds = append(*conds, newCond)
	return *conds
}

func AppendCondition(ctx context.Context, todo *taskv1alpha1.Todo, typeName string, status metav1.ConditionStatus, reason string, message string) []metav1.Condition{
	now := metav1.Now()

	condition := metav1.Condition{
		Type:               typeName,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	}

	return updateCondition(&todo.Status.Condition, condition)
}

func (r *TodoReconciler) CheckDelete(ctx context.Context, todo *taskv1alpha1.Todo, newTodo *v1.Todo) (ctrl.Result, error) {
	l := logf.FromContext(ctx)

	if !todo.ObjectMeta.DeletionTimestamp.IsZero() {
		if ctrlutil.ContainsFinalizer(todo, finalizer) {
			l.Info("Cleaning up Todo:")

			// API Delete TODO
			if newTodo != nil {
				if err := v1.DeleteTodo(newTodo.ID); err != nil {
					l.Error(err, "Failed to delete Todo", "id", todo.Status.ID)
					return ctrl.Result{}, err
				}
			}

			ctrlutil.RemoveFinalizer(todo, finalizer)

			// Update the CR without finalizers, so that it is deleted. 
			if err := r.Update(ctx, todo); err != nil {
				return ctrl.Result{}, err
			}
			l.Info("Removed Finalizer -- Ready to be deleted by K8s")
		}
	}
	return ctrl.Result{}, nil
}

func (r *TodoReconciler) CreateTodo(ctx context.Context, todo *taskv1alpha1.Todo, newTodo *v1.Todo) (*v1.Todo, ctrl.Result, error) {
	l := logf.FromContext(ctx)

	// Only Create if newTodo (API todo) is not present. 

	if newTodo == nil{
		var err error
		newTodo, err = v1.AddTodo(todo.Spec.Title)
		if err != nil {
			l.Error(err, "Failed to add a Todo via API")
			return nil, ctrl.Result{}, err
		}
		l.Info("Added a new Todo via API")
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
		todo.Status.Condition = AppendCondition(ctx, todo, "Reconciled", CONDITION_STATUS_UNKNOWN, "FailedReconciliation", err.Error())
		if serr := r.Status().Update(ctx, todo); serr != nil {
			return ctrl.Result{}, serr
		}
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

	if !todo.ObjectMeta.DeletionTimestamp.IsZero() {
		// CR is being deleted — don't recreate
		return ctrl.Result{}, nil
	}

	// Case 5: Create Todo in external API if not exists
	var result ctrl.Result
	if newTodo, result, err = r.CreateTodo(ctx, todo, newTodo); err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	// Case 6: Update the CR object's Status with the ID 
	// generated by the Todo Add/Create endpoint.
	todo.Status.ID = newTodo.ID
	
	todo.Status.Condition = AppendCondition(ctx, todo, "Reconciled", CONDITION_STATUS_TRUE, "SuccessfulReconciliation", "Successfully reconciled the Todo custom resource.")
	if err := r.Status().Update(ctx, todo); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TodoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&taskv1alpha1.Todo{}).
		Named("todo").
		Complete(r)
}