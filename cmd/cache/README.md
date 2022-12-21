# `cache` wrapper

This binary aims to be used as the image to a *cache* step. The idea
is that given some parameters, it will try to fetch an existing oci
image and extract the cache to the right place.

- `cache fetch --files '**/go.sum' --target quay.io/vdemeest/cache/go-cache:{{hash}} --folder /workspaces/go-cache`
- `cache upload --files '**/go.sum' --target quay.io/vdemeest/cache/go-cache:{{hash}} --folder /workspaces/go-cache`

