# Pele rendering asset

Pinned to Pele 1.13.0. The runtime asset is embedded in `internal/diagram/assets/pele.js`; Node and Bun are build tools only.

Rebuild from the locked npm package:

```sh
npm ci --ignore-scripts --prefix third_party/pele
bun build third_party/pele/entry.js --target browser --format iife --minify --outfile internal/diagram/assets/pele.js
```

The wrapper exposes only diagram source, a fixed local font family and native width measurements. Links, images and user callbacks are disabled. Upstream API: https://pele.run/api
