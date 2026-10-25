# Vendored frontend dependencies

Served from our own origin so the CSP can use `script-src 'self'` (no SRI needed).

| File | Source | Version | sha256 |
|---|---|---|---|
| alpine-3.14.9.min.js | https://cdn.jsdelivr.net/npm/alpinejs@3.14.9/dist/cdn.min.js | 3.14.9 | 3ed1eed252488921df65e363d6715deb04d7f92aaedb9e52199fdf73cb1e0ad3 |
| zxing-0.19.1.min.js | https://unpkg.com/@zxing/library@0.19.1/umd/index.min.js | 0.19.1 | c5837e4858a3775173bab09ee36e6052545c7880c9d7452e2f464770c6e642ce |

To update: download the new pinned version, rename with the version, update index.html and this table.

Tailwind CSS (v3.4.19) is pre-built into `static/tailwind.css`. Rebuild after changing classes in index.html/app.js:

    npx tailwindcss@3.4.19 -c tailwind.config.js -i web/tailwind.input.css -o static/tailwind.css --minify
