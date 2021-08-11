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

package v1alpha2

import (
	"fmt"

	"kmodules.xyz/resource-metrics/api"

	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	api.Register(schema.GroupVersionKind{
		Group:   "kubedb.com",
		Version: "v1alpha2",
		Kind:    "Redis",
	}, Redis{}.ResourceCalculator())
}

type Redis struct{}

func (r Redis) ResourceCalculator() api.ResourceCalculator {
	return &api.ResourceCalculatorFuncs{
		AppRoles:               []api.PodRole{api.DefaultPodRole},
		RuntimeRoles:           []api.PodRole{api.DefaultPodRole, api.ExporterPodRole},
		RoleReplicasFn:         r.roleReplicasFn,
		ModeFn:                 r.modeFn,
		RoleResourceLimitsFn:   r.roleResourceFn(api.ResourceLimits),
		RoleResourceRequestsFn: r.roleResourceFn(api.ResourceRequests),
	}
}

func (r Redis) roleReplicasFn(obj map[string]interface{}) (api.ReplicaList, error) {
	mode, found, err := unstructured.NestedString(obj, "spec", "mode")
	if err != nil {
		return nil, err
	}
	if found && mode == DBModeCluster {
		shards, _, err := unstructured.NestedInt64(obj, "spec", "cluster", "master")
		if err != nil {
			return nil, err
		}
		shardReplicas, _, err := unstructured.NestedInt64(obj, "spec", "cluster", "replicas")
		if err != nil {
			return nil, err
		}
		return api.ReplicaList{
			api.TotalShardPodRole: shards * shardReplicas,
			api.DefaultPodRole:    shards * shardReplicas,
			api.ShardPodRole:      shards,
			api.PerShardPodRole:   shardReplicas,
		}, nil
	}

	// Standalone or sentinel
	replicas, found, err := unstructured.NestedInt64(obj, "spec", "replicas")
	if err != nil {
		return nil, fmt.Errorf("failed to read spec.replicas %v: %w", obj, err)
	}
	if !found {
		return api.ReplicaList{api.DefaultPodRole: 1}, nil
	}
	return api.ReplicaList{api.DefaultPodRole: replicas}, nil
}

func (r Redis) modeFn(obj map[string]interface{}) (string, error) {
	mode, found, err := unstructured.NestedString(obj, "spec", "mode")
	if err != nil {
		return "", err
	}
	if found {
		return mode, nil
	}
	return DBModeStandalone, nil
}

func (r Redis) roleResourceFn(fn func(rr core.ResourceRequirements) core.ResourceList) func(obj map[string]interface{}) (map[api.PodRole]core.ResourceList, error) {
	return func(obj map[string]interface{}) (map[api.PodRole]core.ResourceList, error) {
		exporter, err := api.ContainerResources(obj, fn, "spec", "monitor", "prometheus")
		if err != nil {
			return nil, err
		}

		// Redis Sentinel or Standalone
		container, replicas, err := api.AppNodeResources(obj, fn, "spec")
		if err != nil {
			return nil, err
		}

		mode, found, err := unstructured.NestedString(obj, "spec", "mode")
		if err != nil {
			return nil, err
		}
		if found && mode == DBModeCluster {
			shards, _, err := unstructured.NestedInt64(obj, "spec", "cluster", "master")
			if err != nil {
				return nil, err
			}
			shardReplicas, _, err := unstructured.NestedInt64(obj, "spec", "cluster", "replicas")
			if err != nil {
				return nil, err
			}
			replicas = shards * shardReplicas
		}

		return map[api.PodRole]core.ResourceList{
			api.DefaultPodRole:  api.MulResourceList(container, replicas),
			api.ExporterPodRole: api.MulResourceList(exporter, replicas),
		}, nil
	}
}
