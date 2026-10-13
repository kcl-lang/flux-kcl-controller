import manifests

# Renders a namespaced object on purpose. testdata/crds emits cluster-scoped
# CustomResourceDefinitions, which several KCLRuns in one suite would fight over
# under server-side apply. Namespaced objects in separate test namespaces never
# collide.
#
# The controller does not default the namespace of an applied manifest, so it
# has to come from somewhere. `namespace` is a dynamic top-level variable, read
# back with option() and set per KCLRun through spec.config.arguments:
#
#     spec:
#       config:
#         arguments:
#           - namespace=<ns>
#
# It is deliberately left undeclared. A KCL `option ns = "default"` declaration
# conflicts with the value the controller injects and fails to compile.
items = [
    {
        apiVersion = "v1"
        kind = "ConfigMap"
        metadata = {
            name = "kcl-controller-configmap"
            namespace = option("namespace")
        }
        data = {
            key = "value"
        }
    }
]
manifests.yaml_stream(items)