/*
Copyright AppsCode Inc. and Contributors

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

package api

import (
	"fmt"
	"strings"

	"gomodules.xyz/pointer"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ofst "kmodules.xyz/offshoot-api/api/v1"
)

type ResourceCalculator interface {
	Replicas(obj map[string]interface{}) (int64, error)
	RoleReplicas(obj map[string]interface{}) (ReplicaList, error)

	Mode(obj map[string]interface{}) (string, error)

	TotalResourceLimits(obj map[string]interface{}) (core.ResourceList, error)
	TotalResourceRequests(obj map[string]interface{}) (core.ResourceList, error)

	AppResourceLimits(obj map[string]interface{}) (core.ResourceList, error)
	AppResourceRequests(obj map[string]interface{}) (core.ResourceList, error)

	RoleResourceLimits(obj map[string]interface{}) (map[PodRole]core.ResourceList, error)
	RoleResourceRequests(obj map[string]interface{}) (map[PodRole]core.ResourceList, error)
}

type ResourceCalculatorFuncs struct {
	// Resources used by the main application (eg, database) containers
	AppRoles []PodRole

	// usually AppRoles + Exporter + Any custom sidecar (label selector etc.) that is used at runtime
	// Must NOT include init container resources
	RuntimeRoles []PodRole

	RoleReplicasFn         func(obj map[string]interface{}) (ReplicaList, error)
	ModeFn                 func(obj map[string]interface{}) (string, error)
	RoleResourceLimitsFn   func(obj map[string]interface{}) (map[PodRole]core.ResourceList, error)
	RoleResourceRequestsFn func(obj map[string]interface{}) (map[PodRole]core.ResourceList, error)
}

var _ ResourceCalculator = &ResourceCalculatorFuncs{}

func (c ResourceCalculatorFuncs) Replicas(obj map[string]interface{}) (int64, error) {
	replicas, err := c.RoleReplicas(obj)
	if err != nil {
		return 0, err
	}
	var cnt int64 = 0
	for _, role := range c.AppRoles {
		cnt += replicas[role]
	}
	return cnt, nil
}

func (c ResourceCalculatorFuncs) RoleReplicas(obj map[string]interface{}) (ReplicaList, error) {
	return c.RoleReplicasFn(obj)
}

func (c ResourceCalculatorFuncs) Mode(obj map[string]interface{}) (string, error) {
	if c.ModeFn != nil {
		return c.ModeFn(obj)
	}
	return "", nil
}

func (c ResourceCalculatorFuncs) TotalResourceLimits(obj map[string]interface{}) (core.ResourceList, error) {
	rr, err := c.RoleResourceLimits(obj)
	if err != nil {
		return nil, err
	}
	return MaxResourceList(
		ResourceListForRoles(rr, c.RuntimeRoles),
		ResourceListForRoles(rr, []PodRole{InitPodRole}),
	), nil
}

func (c ResourceCalculatorFuncs) TotalResourceRequests(obj map[string]interface{}) (core.ResourceList, error) {
	rr, err := c.RoleResourceRequests(obj)
	if err != nil {
		return nil, err
	}
	return MaxResourceList(
		ResourceListForRoles(rr, c.RuntimeRoles),
		ResourceListForRoles(rr, []PodRole{InitPodRole}),
	), nil
}

func (c ResourceCalculatorFuncs) AppResourceLimits(obj map[string]interface{}) (core.ResourceList, error) {
	rr, err := c.RoleResourceLimits(obj)
	if err != nil {
		return nil, err
	}
	return ResourceListForRoles(rr, c.AppRoles), nil
}

func (c ResourceCalculatorFuncs) AppResourceRequests(obj map[string]interface{}) (core.ResourceList, error) {
	rr, err := c.RoleResourceRequests(obj)
	if err != nil {
		return nil, err
	}
	return ResourceListForRoles(rr, c.AppRoles), nil
}

func (c ResourceCalculatorFuncs) RoleResourceLimits(obj map[string]interface{}) (map[PodRole]core.ResourceList, error) {
	return c.RoleResourceLimitsFn(obj)
}

func (c ResourceCalculatorFuncs) RoleResourceRequests(obj map[string]interface{}) (map[PodRole]core.ResourceList, error) {
	return c.RoleResourceRequestsFn(obj)
}

func ResourceListForRoles(rr map[PodRole]core.ResourceList, roles []PodRole) core.ResourceList {
	cpu := resource.Quantity{Format: resource.DecimalSI}
	memory := resource.Quantity{Format: resource.BinarySI}
	storage := resource.Quantity{Format: resource.BinarySI}

	var rl core.ResourceList
	for _, role := range roles {
		rl = rr[role]
		cpu.Add(*rl.Cpu())
		memory.Add(*rl.Memory())
		storage.Add(*rl.Storage())
	}

	result := core.ResourceList{}
	if !cpu.IsZero() {
		result[core.ResourceCPU] = cpu
	}
	if !memory.IsZero() {
		result[core.ResourceMemory] = memory
	}
	if !storage.IsZero() {
		result[core.ResourceStorage] = storage
	}
	return result
}

func AddResourceList(x, y core.ResourceList) core.ResourceList {
	result := core.ResourceList{}

	cpu := resource.Quantity{Format: resource.DecimalSI}
	cpu.Add(*x.Cpu())
	cpu.Add(*y.Cpu())
	if !cpu.IsZero() {
		result[core.ResourceCPU] = cpu
	}

	memory := resource.Quantity{Format: resource.BinarySI}
	memory.Add(*x.Memory())
	memory.Add(*y.Memory())
	if !memory.IsZero() {
		result[core.ResourceMemory] = memory
	}

	storage := resource.Quantity{Format: resource.BinarySI}
	storage.Add(*x.Storage())
	storage.Add(*y.Storage())
	if !storage.IsZero() {
		result[core.ResourceStorage] = storage
	}
	return result
}

func MulResourceList(x core.ResourceList, multiplier int64) core.ResourceList {
	result := core.ResourceList{}

	var q *resource.Quantity

	q = x.Cpu()
	if !q.IsZero() {
		n := resource.Quantity{Format: q.Format}
		n.SetMilli(q.MilliValue() * multiplier)
		result[core.ResourceCPU] = n
	}

	q = x.Memory()
	if !q.IsZero() {
		n := resource.Quantity{Format: q.Format}
		n.SetMilli(q.MilliValue() * multiplier)
		result[core.ResourceMemory] = n
	}

	q = x.Storage()
	if !q.IsZero() {
		n := resource.Quantity{Format: q.Format}
		n.SetMilli(q.MilliValue() * multiplier)
		result[core.ResourceStorage] = n
	}

	return result
}

func MaxResourceList(x, y core.ResourceList) core.ResourceList {
	result := core.ResourceList{}
	var q *resource.Quantity

	xCPU, yCPU := x.Cpu(), y.Cpu()
	if xCPU.Cmp(*yCPU) >= 0 {
		q = xCPU
	} else {
		q = yCPU
	}
	if !q.IsZero() {
		result[core.ResourceCPU] = *q
	}

	xMemory, yMemory := x.Memory(), y.Memory()
	if xMemory.Cmp(*yMemory) >= 0 {
		q = xMemory
	} else {
		q = yMemory
	}
	if !q.IsZero() {
		result[core.ResourceMemory] = *q
	}

	xStorage, yStorage := x.Storage(), y.Storage()
	if xStorage.Cmp(*yStorage) >= 0 {
		q = xStorage
	} else {
		q = yStorage
	}
	if !q.IsZero() {
		result[core.ResourceStorage] = *q
	}

	return result
}

func ResourceLimits(rr core.ResourceRequirements) core.ResourceList {
	return rr.Limits
}

func ResourceRequests(rr core.ResourceRequirements) core.ResourceList {
	return rr.Requests
}

type Container struct {
	Resources core.ResourceRequirements `json:"resources"`
}

func AggregateContainerResources(
	obj map[string]interface{},
	fn func(rr core.ResourceRequirements) core.ResourceList,
	aggregate func(x, y core.ResourceList) core.ResourceList,
	fields ...string,
) (core.ResourceList, error) {
	val, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return nil, err
	}
	containers, ok := val.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%v accessor error: %v is of the type %T, expected []interface{}", strings.Join(fields, "."), val, val)
	}

	result := core.ResourceList{}
	for i := range containers {
		container, ok := containers[i].(map[string]interface{})
		if !ok {
			continue
		}

		var c Container
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(container, &c)
		if err != nil {
			return nil, fmt.Errorf("failed to parse container %#v: %w", container, err)
		}
		result = aggregate(result, fn(c.Resources))
	}
	return result, nil
}

func ContainerResources(
	obj map[string]interface{},
	fn func(rr core.ResourceRequirements) core.ResourceList,
	fields ...string,
) (core.ResourceList, error) {
	val, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return nil, err
	}

	var container Container
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(val.(map[string]interface{}), &container)
	if err != nil {
		return nil, fmt.Errorf("failed to parse container %#v: %w", container, err)
	}
	return fn(container.Resources), nil
}

func StorageResources(
	obj map[string]interface{},
	fn func(rr core.ResourceRequirements) core.ResourceList,
	fields ...string,
) (core.ResourceList, error) {
	val, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return nil, err
	}

	var storage core.PersistentVolumeClaimSpec
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(val.(map[string]interface{}), &storage)
	if err != nil {
		return nil, fmt.Errorf("failed to parse storage %#v: %w", storage, err)
	}
	return fn(storage.Resources), nil
}

type AppNode struct {
	Replicas    *int64                         `json:"replicas,omitempty"`
	PodTemplate ofst.PodTemplateSpec           `json:"podTemplate,omitempty"`
	Storage     core.PersistentVolumeClaimSpec `json:"storage,omitempty"`
}

func AppNodeResources(
	obj map[string]interface{},
	fn func(rr core.ResourceRequirements) core.ResourceList,
	fields ...string,
) (core.ResourceList, int64, error) {
	val, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if !found || err != nil {
		return nil, 0, err
	}

	var node AppNode
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(val.(map[string]interface{}), &node)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to parse node %#v: %w", node, err)
	}

	if node.Replicas == nil {
		node.Replicas = pointer.Int64P(1)
	}
	rr := fn(node.PodTemplate.Spec.Resources)
	sr := fn(node.Storage.Resources)
	rr[core.ResourceStorage] = *sr.Storage()

	return rr, *node.Replicas, nil
}
