# ere/amp image

The default image for the `docker` backend.

```bash
task image:build
```

The image installs the Amp CLI into `/opt/amp` at build time, so a sandbox
starts without a network fetch. Pin a release with
`docker build --build-arg AMP_VERSION=<version> .`.

The image holds no credential. `ere up` resolves `AMP_API_KEY` and any
runner secret at launch and writes them into the container over stdin.
