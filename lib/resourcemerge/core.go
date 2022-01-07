package resourcemerge

import (
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
)

// EnsureConfigMap ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureConfigMap(modified *bool, existing *corev1.ConfigMap, required corev1.ConfigMap) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	mergeMap(modified, &existing.Data, required.Data)
}

// EnsureServiceAccount ensures that the existing mathces the required.
// modified is set to true when existing had to be updated with required.
func EnsureServiceAccount(modified *bool, existing *corev1.ServiceAccount, required corev1.ServiceAccount) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	setBoolPtr(modified, &existing.AutomountServiceAccountToken, required.AutomountServiceAccountToken)
}

// ensurePodTemplateSpec ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func ensurePodTemplateSpec(modified *bool, existing *corev1.PodTemplateSpec, required corev1.PodTemplateSpec) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	ensurePodSpec(modified, &existing.Spec, required.Spec)
}

func ensurePodSpec(modified *bool, existing *corev1.PodSpec, required corev1.PodSpec) {
	ensureContainers(modified, &existing.InitContainers, required.InitContainers, required.HostNetwork)
	ensureContainers(modified, &existing.Containers, required.Containers, required.HostNetwork)
	ensureVolumes(modified, &existing.Volumes, required.Volumes)

	if len(required.RestartPolicy) > 0 {
		if existing.RestartPolicy != required.RestartPolicy {
			*modified = true
			existing.RestartPolicy = required.RestartPolicy
		}
	}

	setStringIfSet(modified, &existing.ServiceAccountName, required.ServiceAccountName)
	setBool(modified, &existing.HostNetwork, required.HostNetwork)
	mergeMap(modified, &existing.NodeSelector, required.NodeSelector)
	ensurePodSecurityContextPtr(modified, &existing.SecurityContext, required.SecurityContext)
	ensureAffinityPtr(modified, &existing.Affinity, required.Affinity)
	ensureTolerations(modified, &existing.Tolerations, required.Tolerations)
	setStringIfSet(modified, &existing.PriorityClassName, required.PriorityClassName)
	setInt32Ptr(modified, &existing.Priority, required.Priority)
	setBoolPtr(modified, &existing.ShareProcessNamespace, required.ShareProcessNamespace)
	ensureDNSPolicy(modified, &existing.DNSPolicy, required.DNSPolicy)
	ensureTerminationGracePeriod(modified, &existing.TerminationGracePeriodSeconds, required.TerminationGracePeriodSeconds)
}

func ensureContainers(modified *bool, existing *[]corev1.Container, required []corev1.Container, hostNetwork bool) {
	for i := len(*existing) - 1; i >= 0; i-- {
		existingContainer := &(*existing)[i]
		var existingCurr *corev1.Container
		for _, requiredContainer := range required {
			if existingContainer.Name == requiredContainer.Name {
				existingCurr = &(*existing)[i]
				ensureContainer(modified, existingCurr, requiredContainer, hostNetwork)
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			*existing = append((*existing)[:i], (*existing)[i+1:]...)
		}
	}

	for _, requiredContainer := range required {
		match := false
		for _, existingContainer := range *existing {
			if existingContainer.Name == requiredContainer.Name {
				match = true
				break
			}
		}
		if !match {
			*modified = true
			*existing = append(*existing, requiredContainer)
		}
	}
}

func ensureContainer(modified *bool, existing *corev1.Container, required corev1.Container, hostNetwork bool) {
	setStringIfSet(modified, &existing.Name, required.Name)
	setStringIfSet(modified, &existing.Image, required.Image)

	// if you want modify the launch, you need to modify it in the config, not in the launch args
	setStringSlice(modified, &existing.Command, required.Command)
	setStringSlice(modified, &existing.Args, required.Args)
	ensureEnvVar(modified, &existing.Env, required.Env)
	ensureEnvFromSource(modified, &existing.EnvFrom, required.EnvFrom)
	setStringIfSet(modified, &existing.WorkingDir, required.WorkingDir)
	ensureResourceRequirements(modified, &existing.Resources, required.Resources)
	ensureContainerPorts(modified, &existing.Ports, required.Ports, hostNetwork)
	ensureVolumeMounts(modified, &existing.VolumeMounts, required.VolumeMounts)
	ensureProbePtr(modified, &existing.LivenessProbe, required.LivenessProbe)
	ensureProbePtr(modified, &existing.ReadinessProbe, required.ReadinessProbe)

	// our security context should always win
	ensureSecurityContextPtr(modified, &existing.SecurityContext, required.SecurityContext)
}

func ensureEnvVar(modified *bool, existing *[]corev1.EnvVar, required []corev1.EnvVar) {
	for envidx := range required {
		// Currently only CVO deployment uses this variable to inject internal LB host.
		// This may result in an IP address being returned by API so assuming the
		// returned value is correct.
		if required[envidx].Name == "KUBERNETES_SERVICE_HOST" {
			ensureEnvVarKubeService(*existing, &required[envidx])
		}

		if required[envidx].ValueFrom != nil {
			ensureEnvVarSourceFieldRefDefault(required[envidx].ValueFrom.FieldRef)
		}
	}
	if !equality.Semantic.DeepEqual(required, *existing) {
		*existing = required
		*modified = true
	}
}

func ensureEnvVarKubeService(existing []corev1.EnvVar, required *corev1.EnvVar) {
	for envidx := range existing {
		if existing[envidx].Name == required.Name {
			required.Value = existing[envidx].Value
		}
	}
}

func ensureEnvVarSourceFieldRefDefault(required *corev1.ObjectFieldSelector) {
	if required != nil && required.APIVersion == "" {
		required.APIVersion = "v1"
	}
}

func ensureEnvFromSource(modified *bool, existing *[]corev1.EnvFromSource, required []corev1.EnvFromSource) {
	if !equality.Semantic.DeepEqual(required, *existing) {
		*existing = required
		*modified = true
	}
}

func ensureProbePtr(modified *bool, existing **corev1.Probe, required *corev1.Probe) {
	if *existing == nil && required == nil {
		return
	}
	if *existing == nil || required == nil {
		*modified = true
		*existing = required
		return
	}
	ensureProbeDefaults(required)
	ensureProbe(modified, *existing, *required)
}

func ensureProbeDefaults(required *corev1.Probe) {
	if required.TimeoutSeconds == 0 {
		required.TimeoutSeconds = 1
	}
	if required.PeriodSeconds == 0 {
		required.PeriodSeconds = 10
	}
	if required.SuccessThreshold == 0 {
		required.SuccessThreshold = 1
	}
	if required.FailureThreshold == 0 {
		required.FailureThreshold = 3
	}
}

func ensureProbe(modified *bool, existing *corev1.Probe, required corev1.Probe) {
	setInt32(modified, &existing.InitialDelaySeconds, required.InitialDelaySeconds)
	setInt32(modified, &existing.TimeoutSeconds, required.TimeoutSeconds)
	setInt32(modified, &existing.PeriodSeconds, required.PeriodSeconds)
	setInt32(modified, &existing.SuccessThreshold, required.SuccessThreshold)
	setInt32(modified, &existing.FailureThreshold, required.FailureThreshold)

	ensureProbeHandler(modified, &existing.ProbeHandler, required.ProbeHandler)
}

func ensureProbeHandler(modified *bool, existing *corev1.ProbeHandler, required corev1.ProbeHandler) {
	ensureProbeHandlerDefaults(&required)
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureProbeHandlerDefaults(handler *corev1.ProbeHandler) {
	if handler.HTTPGet != nil && handler.HTTPGet.Scheme == "" {
		handler.HTTPGet.Scheme = corev1.URISchemeHTTP
	}
}

func ensureContainerPorts(modified *bool, existing *[]corev1.ContainerPort, required []corev1.ContainerPort, hostNetwork bool) {
	for i := len(*existing) - 1; i >= 0; i-- {
		existingContainerPort := &(*existing)[i]
		var existingCurr *corev1.ContainerPort
		for _, requiredContainerPort := range required {
			if existingContainerPort.Name == requiredContainerPort.Name {
				existingCurr = &(*existing)[i]
				ensureContainerPort(modified, existingCurr, requiredContainerPort, hostNetwork)
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			*existing = append((*existing)[:i], (*existing)[i+1:]...)
		}
	}
	for _, requiredContainerPort := range required {
		match := false
		for _, existingContainerPort := range *existing {
			if existingContainerPort.Name == requiredContainerPort.Name {
				match = true
				break
			}
		}
		if !match {
			*modified = true
			*existing = append(*existing, requiredContainerPort)
		}
	}
}

func ensureContainerPort(modified *bool, existing *corev1.ContainerPort, required corev1.ContainerPort, hostNetwork bool) {
	ensureContainerPortDefaults(&required, hostNetwork)
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureContainerPortDefaults(containerPort *corev1.ContainerPort, hostNetwork bool) {
	// If HostNetwork is specified and port set to 0, set to match ContainerPort
	if hostNetwork {
		if containerPort.HostPort == 0 {
			containerPort.HostPort = containerPort.ContainerPort
		}
	}
	if containerPort.Protocol == "" {
		containerPort.Protocol = corev1.ProtocolTCP
	}
}

func EnsureServicePorts(modified *bool, existing *[]corev1.ServicePort, required []corev1.ServicePort) {
	for i := len(*existing) - 1; i >= 0; i-- {
		existingServicePort := &(*existing)[i]
		var existingCurr *corev1.ServicePort
		for _, requiredServicePort := range required {
			if existingServicePort.Name == requiredServicePort.Name {
				existingCurr = &(*existing)[i]
				ensureServicePort(modified, existingCurr, requiredServicePort)
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			*existing = append((*existing)[:i], (*existing)[i+1:]...)
		}
	}

	for _, requiredServicePort := range required {
		match := false
		for _, existingServicePort := range *existing {
			if existingServicePort.Name == requiredServicePort.Name {
				match = true
				break
			}
		}
		if !match {
			*modified = true
			*existing = append(*existing, requiredServicePort)
		}
	}
}

func EnsureServiceType(modified *bool, existing *corev1.ServiceType, required corev1.ServiceType) {
	// if we have no required ensure existing is set to default
	if required == "" {
		required = corev1.ServiceTypeClusterIP
	}

	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureServicePort(modified *bool, existing *corev1.ServicePort, required corev1.ServicePort) {
	ensureServicePortDefaults(&required)
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureServicePortDefaults(servicePort *corev1.ServicePort) {
	if servicePort.Protocol == "" {
		servicePort.Protocol = corev1.ProtocolTCP
	}
	if servicePort.TargetPort == intstr.FromInt(0) || servicePort.TargetPort == intstr.FromString("") {
		servicePort.TargetPort = intstr.FromInt(int(servicePort.Port))
	}
}

func ensureVolumeMounts(modified *bool, existing *[]corev1.VolumeMount, required []corev1.VolumeMount) {
	// any volume mount we specify, we require
	exists := struct{}{}
	requiredMountPaths := make(map[string]struct{}, len(required))
	for _, requiredVolumeMount := range required {
		requiredMountPaths[requiredVolumeMount.MountPath] = exists
		var existingCurr *corev1.VolumeMount
		for j, curr := range *existing {
			if curr.MountPath == requiredVolumeMount.MountPath {
				existingCurr = &(*existing)[j]
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			*existing = append(*existing, corev1.VolumeMount{})
			existingCurr = &(*existing)[len(*existing)-1]
		}
		ensureVolumeMount(modified, existingCurr, requiredVolumeMount)
	}

	// any unrecognized volume mount, we remove
	for eidx := len(*existing) - 1; eidx >= 0; eidx-- {
		if _, ok := requiredMountPaths[(*existing)[eidx].MountPath]; !ok {
			*modified = true
			*existing = append((*existing)[:eidx], (*existing)[eidx+1:]...)
		}
	}
}

func ensureVolumeMount(modified *bool, existing *corev1.VolumeMount, required corev1.VolumeMount) {
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureVolumes(modified *bool, existing *[]corev1.Volume, required []corev1.Volume) {
	// any volume we specify, we require.
	exists := struct{}{}
	requiredNames := make(map[string]struct{}, len(required))
	for _, requiredVolume := range required {
		requiredNames[requiredVolume.Name] = exists
		var existingCurr *corev1.Volume
		for j, curr := range *existing {
			if curr.Name == requiredVolume.Name {
				existingCurr = &(*existing)[j]
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			*existing = append(*existing, corev1.Volume{})
			existingCurr = &(*existing)[len(*existing)-1]
		}
		ensureVolume(modified, existingCurr, requiredVolume)
	}

	// any unrecognized volume mount, we remove
	for eidx := len(*existing) - 1; eidx >= 0; eidx-- {
		if _, ok := requiredNames[(*existing)[eidx].Name]; !ok {
			*modified = true
			*existing = append((*existing)[:eidx], (*existing)[eidx+1:]...)
		}
	}
}

func ensureVolume(modified *bool, existing *corev1.Volume, required corev1.Volume) {
	if pointer.AllPtrFieldsNil(&required.VolumeSource) {
		required.VolumeSource = corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		}
	}
	ensureVolumeSourceDefaults(&required.VolumeSource)
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureVolumeSourceDefaults(required *corev1.VolumeSource) {
	if required.HostPath != nil {
		typeVol := corev1.HostPathUnset
		if required.HostPath.Type == nil {
			required.HostPath.Type = &typeVol
		}
	}
	if required.Secret != nil && required.Secret.DefaultMode == nil {
		perm := int32(corev1.SecretVolumeSourceDefaultMode)
		required.Secret.DefaultMode = &perm
	}
	if required.ISCSI != nil && required.ISCSI.ISCSIInterface == "" {
		required.ISCSI.ISCSIInterface = "default"
	}
	if required.RBD != nil {
		if required.RBD.RBDPool == "" {
			required.RBD.RBDPool = "rbd"
		}
		if required.RBD.RadosUser == "" {
			required.RBD.RadosUser = "admin"
		}
		if required.RBD.Keyring == "" {
			required.RBD.Keyring = "/etc/ceph/keyring"
		}
	}
	if required.ConfigMap != nil && required.ConfigMap.DefaultMode == nil {
		perm := int32(corev1.ConfigMapVolumeSourceDefaultMode)
		required.ConfigMap.DefaultMode = &perm
	}
	if required.AzureDisk != nil {
		if required.AzureDisk.CachingMode == nil {
			required.AzureDisk.CachingMode = new(corev1.AzureDataDiskCachingMode)
			*required.AzureDisk.CachingMode = corev1.AzureDataDiskCachingReadWrite
		}
		if required.AzureDisk.Kind == nil {
			required.AzureDisk.Kind = new(corev1.AzureDataDiskKind)
			*required.AzureDisk.Kind = corev1.AzureSharedBlobDisk
		}
		if required.AzureDisk.FSType == nil {
			required.AzureDisk.FSType = new(string)
			*required.AzureDisk.FSType = "ext4"
		}
		if required.AzureDisk.ReadOnly == nil {
			required.AzureDisk.ReadOnly = new(bool)
			*required.AzureDisk.ReadOnly = false
		}
	}
	if required.DownwardAPI != nil && required.DownwardAPI.DefaultMode == nil {
		perm := int32(corev1.DownwardAPIVolumeSourceDefaultMode)
		required.DownwardAPI.DefaultMode = &perm
	}
	if required.Projected != nil && required.Projected.DefaultMode == nil {
		perm := int32(corev1.ProjectedVolumeSourceDefaultMode)
		required.Projected.DefaultMode = &perm
		hour := int64(time.Hour.Seconds())
		for idx := range required.Projected.Sources {
			if required.Projected.Sources[idx].ServiceAccountToken.ExpirationSeconds == nil {
				required.Projected.Sources[idx].ServiceAccountToken.ExpirationSeconds = &hour
			}
		}
	}
	if required.ScaleIO != nil {
		if required.ScaleIO.StorageMode == "" {
			required.ScaleIO.StorageMode = "ThinProvisioned"
		}
		if required.ScaleIO.FSType == "" {
			required.ScaleIO.FSType = "xfs"
		}
	}
}

func ensureSecurityContextPtr(modified *bool, existing **corev1.SecurityContext, required *corev1.SecurityContext) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensureSecurityContext(modified, *existing, *required)
}

func ensureSecurityContext(modified *bool, existing *corev1.SecurityContext, required corev1.SecurityContext) {
	ensureCapabilitiesPtr(modified, &existing.Capabilities, required.Capabilities)
	ensureSELinuxOptionsPtr(modified, &existing.SELinuxOptions, required.SELinuxOptions)
	setBoolPtr(modified, &existing.Privileged, required.Privileged)
	setInt64Ptr(modified, &existing.RunAsUser, required.RunAsUser)
	setBoolPtr(modified, &existing.RunAsNonRoot, required.RunAsNonRoot)
	setBoolPtr(modified, &existing.ReadOnlyRootFilesystem, required.ReadOnlyRootFilesystem)
	setBoolPtr(modified, &existing.AllowPrivilegeEscalation, required.AllowPrivilegeEscalation)
}

func ensureCapabilitiesPtr(modified *bool, existing **corev1.Capabilities, required *corev1.Capabilities) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensureCapabilities(modified, *existing, *required)
}

func ensureCapabilities(modified *bool, existing *corev1.Capabilities, required corev1.Capabilities) {
	// any Add we specify, we require.
	for _, required := range required.Add {
		found := false
		for _, curr := range existing.Add {
			if equality.Semantic.DeepEqual(curr, required) {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.Add = append(existing.Add, required)
		}
	}

	// any Drop we specify, we require.
	for _, required := range required.Drop {
		found := false
		for _, curr := range existing.Drop {
			if equality.Semantic.DeepEqual(curr, required) {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.Drop = append(existing.Drop, required)
		}
	}
}

func setStringSlice(modified *bool, existing *[]string, required []string) {
	if !reflect.DeepEqual(required, *existing) {
		*existing = required
		*modified = true
	}
}

func mergeStringSlice(modified *bool, existing *[]string, required []string) {
	for _, required := range required {
		found := false
		for _, curr := range *existing {
			if required == curr {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			*existing = append(*existing, required)
		}
	}
}

func ensureTolerations(modified *bool, existing *[]corev1.Toleration, required []corev1.Toleration) {
	exists := struct{}{}
	found := make(map[int]struct{}, len(required))
	dups := make(map[int]struct{}, len(*existing))
	for ridx := range required {
		foundAlready := false
		for eidx := range *existing {
			if equality.Semantic.DeepEqual((*existing)[eidx], required[ridx]) {
				if foundAlready {
					dups[eidx] = exists
				}
				foundAlready = true
				found[ridx] = exists
			}
		}
	}
	for eidx := len(*existing) - 1; eidx >= 0; eidx-- { // drop duplicates
		if _, ok := dups[eidx]; ok {
			*modified = true
			*existing = append((*existing)[:eidx], (*existing)[eidx+1:]...)
		}
	}

	for ridx := range required { // append missing
		if _, ok := found[ridx]; !ok {
			*modified = true
			*existing = append((*existing), required[ridx])
		}
	}
}

func ensureAffinityPtr(modified *bool, existing **corev1.Affinity, required *corev1.Affinity) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensureAffinity(modified, *existing, *required)
}

func ensureAffinity(modified *bool, existing *corev1.Affinity, required corev1.Affinity) {
	if !equality.Semantic.DeepEqual(existing.NodeAffinity, required.NodeAffinity) {
		*modified = true
		(*existing).NodeAffinity = required.NodeAffinity
	}
	if !equality.Semantic.DeepEqual(existing.PodAffinity, required.PodAffinity) {
		*modified = true
		(*existing).PodAffinity = required.PodAffinity
	}
	if !equality.Semantic.DeepEqual(existing.PodAntiAffinity, required.PodAntiAffinity) {
		*modified = true
		(*existing).PodAntiAffinity = required.PodAntiAffinity
	}
}

func ensurePodSecurityContextPtr(modified *bool, existing **corev1.PodSecurityContext, required *corev1.PodSecurityContext) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensurePodSecurityContext(modified, *existing, *required)
}

func ensurePodSecurityContext(modified *bool, existing *corev1.PodSecurityContext, required corev1.PodSecurityContext) {
	ensureSELinuxOptionsPtr(modified, &existing.SELinuxOptions, required.SELinuxOptions)
	setInt64Ptr(modified, &existing.RunAsUser, required.RunAsUser)
	setInt64Ptr(modified, &existing.RunAsGroup, required.RunAsGroup)
	setBoolPtr(modified, &existing.RunAsNonRoot, required.RunAsNonRoot)

	// any SupplementalGroups we specify, we require.
	for _, required := range required.SupplementalGroups {
		found := false
		for _, curr := range existing.SupplementalGroups {
			if curr == required {
				found = true
				break
			}
		}
		if !found {
			*modified = true
			existing.SupplementalGroups = append(existing.SupplementalGroups, required)
		}
	}

	setInt64Ptr(modified, &existing.FSGroup, required.FSGroup)

	// any SupplementalGroups we specify, we require.
	for _, required := range required.Sysctls {
		found := false
		for j, curr := range existing.Sysctls {
			if curr.Name == required.Name {
				found = true
				if curr.Value != required.Value {
					*modified = true
					existing.Sysctls[j] = required
				}
				break
			}
		}
		if !found {
			*modified = true
			existing.Sysctls = append(existing.Sysctls, required)
		}
	}
}

func ensureSELinuxOptionsPtr(modified *bool, existing **corev1.SELinuxOptions, required *corev1.SELinuxOptions) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	ensureSELinuxOptions(modified, *existing, *required)
}

func ensureSELinuxOptions(modified *bool, existing *corev1.SELinuxOptions, required corev1.SELinuxOptions) {
	setStringIfSet(modified, &existing.User, required.User)
	setStringIfSet(modified, &existing.Role, required.Role)
	setStringIfSet(modified, &existing.Type, required.Type)
	setStringIfSet(modified, &existing.Level, required.Level)
}

func ensureResourceRequirements(modified *bool, existing *corev1.ResourceRequirements, required corev1.ResourceRequirements) {
	ensureResourceList(modified, &existing.Limits, &required.Limits)
	ensureResourceList(modified, &existing.Requests, &required.Requests)
}

func ensureResourceList(modified *bool, existing *corev1.ResourceList, required *corev1.ResourceList) {
	if !equality.Semantic.DeepEqual(existing, required) {
		*modified = true
		required.DeepCopyInto(existing)
	}
}

func ensureDNSPolicy(modified *bool, existing *corev1.DNSPolicy, required corev1.DNSPolicy) {
	if required == "" {
		required = corev1.DNSClusterFirst
	}
	if !equality.Semantic.DeepEqual(required, *existing) {
		*modified = true
		*existing = required
	}
}

func ensureTerminationGracePeriod(modified *bool, existing **int64, required *int64) {
	if required == nil {
		period := int64(corev1.DefaultTerminationGracePeriodSeconds)
		required = &period
	}
	setInt64Ptr(modified, existing, required)
}

func setBool(modified *bool, existing *bool, required bool) {
	if required != *existing {
		*existing = required
		*modified = true
	}
}

func setBoolPtr(modified *bool, existing **bool, required *bool) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	setBool(modified, *existing, *required)
}

func setInt32(modified *bool, existing *int32, required int32) {
	if required != *existing {
		*existing = required
		*modified = true
	}
}

func setInt32Ptr(modified *bool, existing **int32, required *int32) {
	if *existing == nil && required == nil {
		return
	}
	if *existing == nil || (required == nil && *existing != nil) {
		*modified = true
		*existing = required
		return
	}
	setInt32(modified, *existing, *required)
}

func setInt64(modified *bool, existing *int64, required int64) {
	if required != *existing {
		*existing = required
		*modified = true
	}
}

func setInt64Ptr(modified *bool, existing **int64, required *int64) {
	// if we have no required, then we don't care what someone else has set
	if required == nil {
		return
	}

	if *existing == nil {
		*modified = true
		*existing = required
		return
	}
	setInt64(modified, *existing, *required)
}
