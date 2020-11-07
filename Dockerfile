FROM gcr.io/distroless/base:debug
SHELL ["/busybox/sh", "-c"]
RUN mkdir -p /var/log/contrail
COPY contrail-init /

