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
		Kind:    "MongoDB",
	}, MongoDB{}.ResourceCalculator())
}

type MongoDB struct{}

func (r MongoDB) ResourceCalculator() api.ResourceCalculator {
	return &api.ResourceCalculatorFuncs{
		AppRoles:               []api.PodRole{api.DefaultPodRole, api.TotalShardPodRole, api.ConfigServerPodRole, api.MongosPodRole},
		RuntimeRoles:           []api.PodRole{api.DefaultPodRole, api.TotalShardPodRole, api.ConfigServerPodRole, api.MongosPodRole, api.ExporterPodRole},
		RoleReplicasFn:         r.roleReplicasFn,
		ModeFn:                 r.modeFn,
		RoleResourceLimitsFn:   r.roleResourceFn(api.ResourceLimits),
		RoleResourceRequestsFn: r.roleResourceFn(api.ResourceRequests),
	}
}

func (r MongoDB) roleReplicasFn(obj map[string]interface{}) (api.ReplicaList, error) {
	// Sharded MongoDB cluster
	shardTopology, found, err := unstructured.NestedMap(obj, "spec", "shardTopology")
	if err != nil {
		return nil, err
	}
	if found && shardTopology != nil {
		shards, _, err := unstructured.NestedInt64(shardTopology, "shard", "shards")
		if err != nil {
			return nil, err
		}
		shardReplicas, _, err := unstructured.NestedInt64(shardTopology, "shard", "replicas")
		if err != nil {
			return nil, err
		}
		configServerReplicas, _, err := unstructured.NestedInt64(shardTopology, "configServer", "replicas")
		if err != nil {
			return nil, err
		}
		mongosReplicas, _, err := unstructured.NestedInt64(shardTopology, "mongos", "replicas")
		if err != nil {
			return nil, err
		}
		return api.ReplicaList{
			api.TotalShardPodRole:   shards * shardReplicas,
			api.ShardPodRole:        shards,
			api.PerShardPodRole:     shardReplicas,
			api.ConfigServerPodRole: configServerReplicas,
			api.MongosPodRole:       mongosReplicas,
		}, nil
	}

	// MongoDB ReplicaSet or Standalone
	replicas, found, err := unstructured.NestedInt64(obj, "spec", "replicas")
	if err != nil {
		return nil, fmt.Errorf("failed to read spec.replicas %v: %w", obj, err)
	}
	if !found {
		return api.ReplicaList{api.DefaultPodRole: 1}, nil
	}
	return api.ReplicaList{api.DefaultPodRole: replicas}, nil
}

func (r MongoDB) modeFn(obj map[string]interface{}) (string, error) {
	shards, found, err := unstructured.NestedMap(obj, "spec", "shardTopology")
	if err != nil {
		return "", err
	}
	if found && shards != nil {
		return DBModeSharded, nil
	}
	rs, found, err := unstructured.NestedMap(obj, "spec", "replicaSet")
	if err != nil {
		return "", err
	}
	if found && rs != nil {
		return DBModeReplicaSet, nil
	}
	return DBModeStandalone, nil
}

func (r MongoDB) roleResourceFn(fn func(rr core.ResourceRequirements) core.ResourceList) func(obj map[string]interface{}) (map[api.PodRole]core.ResourceList, error) {
	return func(obj map[string]interface{}) (map[api.PodRole]core.ResourceList, error) {
		exporter, err := api.ContainerResources(obj, fn, "spec", "monitor", "prometheus")
		if err != nil {
			return nil, err
		}

		// Sharded MongoDB
		shardTopology, found, err := unstructured.NestedMap(obj, "spec", "shardTopology")
		if err != nil {
			return nil, err
		}
		if found && shardTopology != nil {
			// Shard nodes resources
			shards, _, err := unstructured.NestedInt64(shardTopology, "shard", "shards")
			if err != nil {
				return nil, err
			}
			shard, shardReplicas, err := api.AppNodeResources(shardTopology, fn, "shard")
			if err != nil {
				return nil, err
			}

			// ConfigServer nodes resources
			configServer, configServerReplicas, err := api.AppNodeResources(shardTopology, fn, "configServer")
			if err != nil {
				return nil, err
			}

			// Mongos node resources
			mongos, mongosReplicas, err := api.AppNodeResources(shardTopology, fn, "mongos")
			if err != nil {
				return nil, err
			}

			return map[api.PodRole]core.ResourceList{
				api.TotalShardPodRole:   api.MulResourceList(shard, shards*shardReplicas),
				api.ConfigServerPodRole: api.MulResourceList(configServer, configServerReplicas),
				api.MongosPodRole:       api.MulResourceList(mongos, mongosReplicas),
				api.ExporterPodRole:     api.MulResourceList(exporter, shards*shardReplicas+configServerReplicas+mongosReplicas),
			}, nil
		}

		// MongoDB ReplicaSet or Standalone
		container, replicas, err := api.AppNodeResources(obj, fn, "spec")
		if err != nil {
			return nil, err
		}

		return map[api.PodRole]core.ResourceList{
			api.DefaultPodRole:  api.MulResourceList(container, replicas),
			api.ExporterPodRole: api.MulResourceList(exporter, replicas),
		}, nil
	}
}
