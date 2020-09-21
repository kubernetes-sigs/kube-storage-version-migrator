# kube-storage-version-migrator

This tool migrates stored data in etcd to the latest storage version.

To start using the storage version migrator, see the [user guide](USER_GUIDE.md).

Related KEPs:

* [Prototype design](https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/0030-storage-migration.md)
* [Automated migration](https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/storage-migration-auto-trigger.md)

## Community, discussion, contribution, and support

Learn how to engage with the Kubernetes community on the [community page](http://kubernetes.io/community/).

You can reach the maintainers of this project at:

- [Slack](https://kubernetes.slack.com/messages/sig-api-machinery)
- [Mailing List](https://groups.google.com/forum/#!forum/kubernetes-sig-api-machinery)

### Code of conduct

Participation in the Kubernetes community is governed by the [Kubernetes Code of Conduct](code-of-conduct.md).

## Build

Please make sure you have the REGISTRY environment variable set.
For local builds it can be set to anything.
For image builds it determines the location of your image.
The REGISTRY defaults to `gcr.io/$(shell gcloud config get-value project)`.

## Published images

Alternatively, you can use the published images in your manifests. The latest
images are pushed to the following Kubernetes production repos:

- us.gcr.io/k8s-artifacts-prod/storage-migrator
- eu.gcr.io/k8s-artifacts-prod/storage-migrator
- asia.gcr.io/k8s-artifacts-prod/storage-migrator
