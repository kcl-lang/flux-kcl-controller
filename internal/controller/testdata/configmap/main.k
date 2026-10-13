import manifests

# Renders a namespaced object on purpose. testdata/crds emits cluster-scoped
# CustomResourceDefinitions, which several KCLRuns in one suite would fight over
# under server-side apply. Namespaced objects in separate test namespaces never
# collide.
items = [
    {
        apiVersion = "v1"
        kind = "ConfigMap"
        metadata = {
            name = "kcl-controller-configmap"
        }
        data = {
            key = "value"
        }
    }
]
manifests.yaml_stream(items)