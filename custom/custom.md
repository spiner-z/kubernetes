# Custom

## kube-scheduler

vendor:

```bash
# pwd: kubernetes
make vendor
```

compile:

```bash
# pwd: kubernetes
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
# pwd: kubernetes

docker build -f Dockerfile -t kube-scheduler:custom-1.35 .

# Or:
# docker build -f Dockerfile -t <repo>/kube-scheduler:custom-1.35 .
```
