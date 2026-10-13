import manifests

# Same namespaced output as testdata/configmap, and for the same reason: this
# package exists to exercise spec.package.version, so all that matters about
# what it renders is that it is safe to apply repeatedly across test namespaces,
# which a cluster-scoped object is not.
#
# The namespace comes in as a dynamic top-level variable, exactly as in
# testdata/configmap.
items = [
    {
        apiVersion = "v1"
        kind = "ConfigMap"
        metadata = {
            name = "kcl-controller-pkg"
            namespace = option("namespace")
        }
        data = {
            key = "value"
        }
    }
]
manifests.yaml_stream(items)
