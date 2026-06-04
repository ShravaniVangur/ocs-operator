package storagecluster

import (
	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ocsv1 "github.com/red-hat-storage/ocs-operator/api/v4/v1"
)

const (
	storageClusterDeletePolicyName        = "storagecluster-delete-protection.ocs.openshift.io"
	storageClusterDeletePolicyBindingName = "storagecluster-delete-protection-binding.ocs.openshift.io"
)

type ocsValidatingAdmissionPolicy struct{}

// The deletion of StorageCluster must only be permitted when the annotation "uninstall.ocs.openshift.io/confirm-deletion: true" is set on the StorageCluster.
// This is to prevent accidental deletion of the StorageCluster and ensure that the user has explicitly confirmed the deletion.
func (o *ocsValidatingAdmissionPolicy) ensureCreated(r *StorageClusterReconciler, _ *ocsv1.StorageCluster) (reconcile.Result, error) {
	failurePolicy := admissionv1.Fail
	vap := &admissionv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageClusterDeletePolicyName,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(r.ctx, r.Client, vap, func() error {
		vap.Spec = admissionv1.ValidatingAdmissionPolicySpec{
			FailurePolicy: &failurePolicy,
			MatchConstraints: &admissionv1.MatchResources{
				ResourceRules: []admissionv1.NamedRuleWithOperations{
					{
						RuleWithOperations: admissionv1.RuleWithOperations{
							Operations: []admissionv1.OperationType{admissionv1.Delete},
							Rule: admissionv1.Rule{
								APIGroups:   []string{"ocs.openshift.io"},
								APIVersions: []string{"v1"},
								Resources:   []string{"storageclusters"},
							},
						},
					},
				},
			},
			Validations: []admissionv1.Validation{
				{
					Expression: `oldObject.metadata.?annotations['uninstall.ocs.openshift.io/confirm-deletion'].orValue('') == 'true'`,
					Message:    `StorageCluster deletion is blocked. Set the annotation "uninstall.ocs.openshift.io/confirm-deletion: true" on the StorageCluster to allow deletion.`,
					Reason:     ptr.To(metav1.StatusReasonForbidden),
				},
			},
		}
		return nil
	}); err != nil {
		return reconcile.Result{}, err
	}

	vapBinding := &admissionv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageClusterDeletePolicyBindingName,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(r.ctx, r.Client, vapBinding, func() error {
		vapBinding.Spec = admissionv1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName:        storageClusterDeletePolicyName,
			ValidationActions: []admissionv1.ValidationAction{admissionv1.Deny},
		}
		return nil
	}); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (o *ocsValidatingAdmissionPolicy) ensureDeleted(r *StorageClusterReconciler, sc *ocsv1.StorageCluster) (reconcile.Result, error) {
	for _, other := range r.clusters.GetStorageClusters() {
		if other.Name != sc.Name && other.Status.Phase != ocsv1.PhaseIgnored {
			return reconcile.Result{}, nil
		}
	}

	vapBinding := &admissionv1.ValidatingAdmissionPolicyBinding{}
	vapBinding.Name = storageClusterDeletePolicyBindingName
	if err := r.Get(r.ctx, client.ObjectKeyFromObject(vapBinding), vapBinding); client.IgnoreNotFound(err) != nil {
		return reconcile.Result{}, err
	} else if vapBinding.UID != "" {
		if err := r.Delete(r.ctx, vapBinding); client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, err
		}
	}

	vap := &admissionv1.ValidatingAdmissionPolicy{}
	vap.Name = storageClusterDeletePolicyName
	if err := r.Get(r.ctx, client.ObjectKeyFromObject(vap), vap); client.IgnoreNotFound(err) != nil {
		return reconcile.Result{}, err
	} else if vap.UID != "" {
		if err := r.Delete(r.ctx, vap); client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}
