package storagecluster

import (
	"context"
	"fmt"
	"maps"
	"slices"

	ocsv1 "github.com/red-hat-storage/ocs-operator/api/v4/v1"
	ocsv1a1 "github.com/red-hat-storage/ocs-operator/api/v4/v1alpha1"
	"github.com/red-hat-storage/ocs-operator/v4/controllers/defaults"
	"github.com/red-hat-storage/ocs-operator/v4/controllers/util"

	groupsnapapi "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumegroupsnapshot/v1beta1"
	snapapi "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	localStorageConsumerConfigMapName = "storageconsumer-internal"
)

var (
	supportedCsiDrivers = []string{
		util.RbdDriverName,
		util.CephFSDriverName,
		util.NfsDriverName,
	}
)

type storageConsumer struct{}

var _ resourceManager = &storageConsumer{}

func (s *storageConsumer) ensureCreated(r *StorageClusterReconciler, storageCluster *ocsv1.StorageCluster) (ctrl.Result, error) {

	storageClassesSpec, err := getLocalStorageClassNames(r.ctx, r.Client, storageCluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate storageclasses list for distribution: %v", err)
	}
	volumeSnapshotClassesSpec, err := getLocalVolumeSnapshotClassNames(r.ctx, r.Client, storageCluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate volumesnapshotclasses list for distribution: %v", err)
	}
	volumeGroupSnapshotClassesSpec, err := getLocalVolumeGroupSnapshotClassNames(r.ctx, r.Client, storageCluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate volumegroupsnapshotclasses list for distribution: %v", err)
	}

	storageConsumer := &ocsv1a1.StorageConsumer{}
	storageConsumer.Name = defaults.LocalStorageConsumerName
	storageConsumer.Namespace = storageCluster.Namespace
	if _, err := controllerutil.CreateOrUpdate(r.ctx, r.Client, storageConsumer, func() error {
		if err := controllerutil.SetControllerReference(storageCluster, storageConsumer, r.Scheme); err != nil {
			return err
		}
		spec := &storageConsumer.Spec
		spec.ResourceNameMappingConfigMap.Name = localStorageConsumerConfigMapName
		spec.StorageClasses = storageClassesSpec
		spec.VolumeSnapshotClasses = volumeSnapshotClassesSpec
		spec.VolumeGroupSnapshotClasses = volumeGroupSnapshotClassesSpec

		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create/update storageconsumer %s: %v", storageConsumer.Name, err)
	}
	if storageConsumer.UID == "" {
		return ctrl.Result{}, fmt.Errorf("expected storageConsumer UID to not be empty")
	}

	availableServices, err := util.GetAvailableServices(r.ctx, r.Client, storageCluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get available services configured in StorageCluster: %v", err)
	}
	consumerConfigMap := &corev1.ConfigMap{}
	consumerConfigMap.Name = localStorageConsumerConfigMapName
	consumerConfigMap.Namespace = storageCluster.Namespace
	if _, err := controllerutil.CreateOrUpdate(r.ctx, r.Client, consumerConfigMap, func() error {
		data := util.GetStorageConsumerDefaultResourceNames(
			defaults.LocalStorageConsumerName,
			string(storageConsumer.UID),
			availableServices,
		)
		resourceMap := util.WrapStorageConsumerResourceMap(data)
		resourceMap.ReplaceRbdRadosNamespaceName(util.ImplicitRbdRadosNamespaceName)
		resourceMap.ReplaceSubVolumeGroupName("csi")
		resourceMap.ReplaceSubVolumeGroupRadosNamespaceName("csi")
		resourceMap.ReplaceRbdClientProfileName("openshift-storage")
		resourceMap.ReplaceCephFsClientProfileName("openshift-storage")
		resourceMap.ReplaceNfsClientProfileName("openshift-storage")
		// NB: Do we need to allow user changing/overwriting any values in this configmap?
		consumerConfigMap.Data = data
		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create/update storageconsumer configmap %s: %v", localStorageConsumerConfigMapName, err)
	}

	return ctrl.Result{}, nil
}

func (s *storageConsumer) ensureDeleted(_ *StorageClusterReconciler, _ *ocsv1.StorageCluster) (ctrl.Result, error) {
	// cleaned up via owner references
	return ctrl.Result{}, nil
}

func getLocalStorageClassNames(ctx context.Context, kubeClient client.Client, storageCluster *ocsv1.StorageCluster) (
	[]ocsv1a1.StorageClassSpec, error) {

	storageClassNames := map[string]bool{}
	storageClassNames[util.GenerateNameForCephBlockPoolStorageClass(storageCluster)] = true
	storageClassNames[util.GenerateNameForCephFilesystemStorageClass(storageCluster)] = true

	crd := &metav1.PartialObjectMetadata{}
	crd.SetGroupVersionKind(extv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"))
	crd.Name = VirtualMachineCrdName
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(crd), crd); client.IgnoreNotFound(err) != nil {
		return nil, err
	}
	if crd.UID != "" {
		storageClassNames[util.GenerateNameForCephBlockPoolVirtualizationStorageClass(storageCluster)] = true
	}

	if storageCluster.Spec.ManagedResources.CephNonResilientPools.Enable {
		storageClassNames[util.GenerateNameForNonResilientCephBlockPoolStorageClass(storageCluster)] = true
	}

	if storageCluster.Spec.NFS != nil && storageCluster.Spec.NFS.Enable {
		storageClassNames[util.GenerateNameForCephNetworkFilesystemStorageClass(storageCluster)] = true
	}

	if storageCluster.Spec.Encryption.StorageClass && storageCluster.Spec.Encryption.KeyManagementService.Enable {
		storageClassNames[util.GenerateNameForEncryptedCephBlockPoolStorageClass(storageCluster)] = true
	}

	// for day2 storageclasses
	storageClassesInCluster := &storagev1.StorageClassList{}
	if err := kubeClient.List(ctx, storageClassesInCluster); err != nil {
		return nil, err
	}
	for idx := range storageClassesInCluster.Items {
		// TODO: skip storageclasses that are from external mode if both internal & external mode is enabled
		sc := &storageClassesInCluster.Items[idx]
		if slices.Contains(supportedCsiDrivers, sc.Provisioner) {
			storageClassNames[sc.Name] = true
		}
	}

	scSpec := make([]ocsv1a1.StorageClassSpec, 0, len(storageClassNames))
	for scName := range maps.Keys(storageClassNames) {
		// TODO: store the spec as the value which allows each value to be customizable corresponding to the name,
		// not doing now as we only have name in the storageclassspec
		scSpec = append(scSpec, ocsv1a1.StorageClassSpec{Name: scName})
	}
	return scSpec, nil
}

func getLocalVolumeSnapshotClassNames(ctx context.Context, kubeClient client.Client, storageCluster *ocsv1.StorageCluster) (
	[]ocsv1a1.VolumeSnapshotClassSpec, error) {

	volumeSnapshotClassNames := map[string]bool{}
	volumeSnapshotClassNames[util.GenerateNameForSnapshotClass(storageCluster.Name, util.RbdSnapshotter)] = true
	volumeSnapshotClassNames[util.GenerateNameForSnapshotClass(storageCluster.Name, util.CephfsSnapshotter)] = true

	if storageCluster.Spec.NFS != nil && storageCluster.Spec.NFS.Enable {
		volumeSnapshotClassNames[util.GenerateNameForSnapshotClass(storageCluster.Name, util.NfsSnapshotter)] = true
	}

	// for day2 volumesnapshotclasses
	volumeSnapshotClassesInCluster := &snapapi.VolumeSnapshotClassList{}
	if err := kubeClient.List(ctx, volumeSnapshotClassesInCluster); err != nil {
		return nil, err
	}
	for idx := range volumeSnapshotClassesInCluster.Items {
		// TODO: skip volumesnapshotclasses that are from external mode if both internal & external mode is enabled
		vsc := &volumeSnapshotClassesInCluster.Items[idx]
		if slices.Contains(supportedCsiDrivers, vsc.Driver) {
			volumeSnapshotClassNames[vsc.Name] = true
		}
	}

	vscSpec := make([]ocsv1a1.VolumeSnapshotClassSpec, 0, len(volumeSnapshotClassNames))
	for vscName := range maps.Keys(volumeSnapshotClassNames) {
		vscSpec = append(vscSpec, ocsv1a1.VolumeSnapshotClassSpec{Name: vscName})
	}
	return vscSpec, nil
}

func getLocalVolumeGroupSnapshotClassNames(ctx context.Context, kubeClient client.Client, storageCluster *ocsv1.StorageCluster) (
	[]ocsv1a1.VolumeGroupSnapshotClassSpec, error) {

	volumeGroupSnapshotClassNames := map[string]bool{}
	volumeGroupSnapshotClassNames[util.GenerateNameForGroupSnapshotClass(storageCluster, util.RbdGroupSnapshotter)] = true
	volumeGroupSnapshotClassNames[util.GenerateNameForGroupSnapshotClass(storageCluster, util.CephfsGroupSnapshotter)] = true

	crd := &metav1.PartialObjectMetadata{}
	crd.SetGroupVersionKind(extv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"))
	crd.Name = VolumeGroupSnapshotClassCrdName
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(crd), crd); client.IgnoreNotFound(err) != nil {
		return nil, err
	}
	if crd.UID != "" {
		// for day2 volumegroupsnapshotclasses
		volumeGroupSnapshotClassesInCluster := &groupsnapapi.VolumeGroupSnapshotClassList{}
		if err := kubeClient.List(ctx, volumeGroupSnapshotClassesInCluster); err != nil {
			return nil, err
		}
		for idx := range volumeGroupSnapshotClassesInCluster.Items {
			// TODO: skip volumegroupsnapshotclasses that are from external mode if both internal & external mode is enabled
			vgsc := &volumeGroupSnapshotClassesInCluster.Items[idx]
			if slices.Contains(supportedCsiDrivers, vgsc.Driver) {
				volumeGroupSnapshotClassNames[vgsc.Name] = true
			}
		}
	}

	vgscSpec := make([]ocsv1a1.VolumeGroupSnapshotClassSpec, 0, len(volumeGroupSnapshotClassNames))
	for vscName := range maps.Keys(volumeGroupSnapshotClassNames) {
		vgscSpec = append(vgscSpec, ocsv1a1.VolumeGroupSnapshotClassSpec{Name: vscName})
	}
	return vgscSpec, nil
}
