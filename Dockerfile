FROM gcr.io/distroless/base

ENV APISERVER_CERT_LOCATION=/far-edge-kubelet-cert.pem
ENV APISERVER_KEY_LOCATION=/far-edge-kubelet-key.pem
ENV KUBELET_PORT=10250

# Use the pre-built binary in "bin/far-edge-kubelet".
COPY far-edge-kubelet /far-edge-kubelet
# Copy the configuration file for the mock provider.
COPY ./deploy/configuration-data/cfg.json /far-edge-kubelet-cfg.json
# Copy the certificate for the HTTPS server.
COPY ./deploy/configuration-data/cert.pem /far-edge-kubelet-cert.pem
# Copy the private key for the HTTPS server.
COPY ./deploy/configuration-data/private-key.pem /far-edge-kubelet-key.pem

CMD ["/far-edge-kubelet", "--provider-config", "/far-edge-kubelet-cfg.json", "--klog.v", "5"]