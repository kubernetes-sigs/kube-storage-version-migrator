# User Guide

* [Who needs to use storage version migrator?](#who-needs-to-use-storage-version-migrator)
* [Storage version migrator in a nutshell](#storage-version-migrator-in-a-nutshell)
* [Deploy the Storage Version Migrator in your cluster](#deploy-the-storage-version-migrator-in-your-cluster)
* [Check if migration has completed](#check-if-migration-has-completed)

## Who needs to use storage version migrator?

If you ever plan to upgrade (or downgrade) the API server in you Kubernetes
cluster, start using the storage version migrator now!

Otherwise, the stored objects could be encoded in an old version, then there is
no guarantee that the upgraded (or downgraded) API server still understands that
version.

The storage version migrator provided in this repo migrates objects to the
latest storage version. It is safe to upgrade (or downgrade) the API server only
after the migration has completed.

## Storage version migrator in a nutshell

The migrator is composed of two controllers: the trigger controller and the
actual migration controller.

The trigger controller
* Detects changes of the default storage version of a resource type by polling
  the API server's [discovery document][] every 10 mins.
* Creates [migration requests][] for resource types whose storage version changes.

The trigger controller also creates migration requests for all resources when it
is first installed in a cluster. This means that if the is migrator installed at
a point where some resources are already stored in etcd at a legacy version, it
_will_ migrate those resources.

The migration controller processes the migration requests one by one. When migrating
a resource type, for all objects of that resource type, the migration controller
gets the object, then writes it back to the API server without modification. The
purpose is to trigger the API server to encode the object in the latest storage
version before storing it.

Please refer to the [KEP][] for details.

[discovery document]:https://github.com/kubernetes/kubernetes/blob/7351f1acd7e67c46112a988a70a01ce46775707e/staging/src/k8s.io/apimachinery/pkg/apis/meta/v1/types.go#L986
[migration requests]:https://github.com/kubernetes-sigs/kube-storage-version-migrator/blob/60dee538334c2366994c2323c0db5db8ab4d2838/pkg/apis/migration/v1alpha1/types.go#L30
[KEP]:https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/0030-storage-migration.md

## Deploy the Storage Version Migrator in your cluster
Prerequisites:
* Your API server version must be at least v1.15, with the
`StorageVersionHash` feature enabled (it is on by default starting v1.15).
* Your `kubectl` version must at least v1.14, where `kubectl apply -k` started
  being supported.
* You have PATCH and CREATE permissions to namespaces, cluster roles, cluster
  role bindings, CRDs, and deployments.

Follow these steps to deploy:
* Clone this repo.
* Go to the root of this repo.
* Run `make local-manifests`.
* Run `pushd manifests.local && kubectl apply -k ./ && popd`.

By default, the trigger controller and migration controller are deployed in the
`kube-system` namespace. If you want to deploy them in a different namespaces,
setup the `NAMEPSPACE` environment variable before running the commands above.

## Check if migration has completed

It is safe to upgrade (downgrade) the API server only after the storage version
migration has completed. To check that, run

```
kubectl get storageversionmigrations -o=custom-columns=NAME:.spec.resource.resource,STATUS:.status.conditions[0].type
```

and see if the status of all migrations are "SUCCEEDED".
