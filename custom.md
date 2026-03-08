# Custom

## kube-scheduler

vendor:

```bash
make vendor
```

compile:

```bash
make WHAT=cmd/kube-scheduler
```

dockerfile:

```Dockerfile
FROM gcr.io/distroless/static:latest

COPY _output/bin/kube-scheduler /usr/local/bin/kube-scheduler

ENTRYPOINT ["/usr/local/bin/kube-scheduler"]
```

build

```bash
docker build -f Dockerfile -t <repo>/kube-scheduler:custom-1.35 .
```
