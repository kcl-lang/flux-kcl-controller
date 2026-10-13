import manifests

# The literal the marker annotates is rewritten to whatever the policy resolves
# to; the unmarked one below it is left alone.
image = "ghcr.io/foo/app:1.0.0"  # {"$imagepolicy": "app"}
pinned = "ghcr.io/foo/app:1.0.0"

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