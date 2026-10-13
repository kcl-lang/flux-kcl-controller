import manifests

# The ImagePolicy named in the marker below keeps this reference current. The
# controller resolves the policy and rewrites the literal before compiling, so
# publishing a new tag needs no edit here.
image = "ghcr.io/foo/app:1.0.0"  # {"$imagepolicy": "app"}

# An unmarked occurrence of the same image, to show that only the literal the
# marker annotates is touched.
pinned = "ghcr.io/foo/app:1.0.0"

# The controller does not default the namespace of an applied manifest, so it has
# to come from somewhere. `namespace` is a dynamic top-level variable, read back
# with option() and set per KCLRun through spec.config.arguments:
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
            name = "kcl-controller-image"
            namespace = option("namespace")
        }
        data = {
            image = image
            pinned = pinned
        }
    }
]
manifests.yaml_stream(items)