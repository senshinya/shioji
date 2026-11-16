# usvg rendering runtime

Official usvg 0.50.0 for Apple Silicon macOS. `runtime.json` pins both the official release archive and its executable by SHA-256. No executable is committed here.

`python3 scripts/bundle-diagram.py` prepares `build/diagram-runtime/usvg` for local execution. Passing a resources directory copies the verified executable and licenses into an application bundle before signing. Rendering never downloads dependencies.

Source and releases: https://github.com/linebender/resvg/releases/tag/v0.50.0

The library and C API can be used under MIT or Apache-2.0; this bundle includes the MIT notice. System fonts remain on the user's Mac and are not redistributed.
