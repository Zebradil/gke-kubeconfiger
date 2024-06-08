FROM scratch
COPY --from=alpine:20240606 --link /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY gke-kubeconfiger /
ENTRYPOINT ["/gke-kubeconfiger"]
