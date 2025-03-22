FROM scratch
COPY --from=alpine:20250108@sha256:115729ec5cb049ba6359c3ab005ac742012d92bbaa5b8bc1a878f1e8f62c0cb8 --link /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY gke-kubeconfiger /
ENTRYPOINT ["/gke-kubeconfiger"]
