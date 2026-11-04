# The OpenAgentCore Runtime image for OpenSandbox: OpenSandbox runs the image's
# user, so the Runtime layout is prepared here and the sandbox needs no root.
ARG RUNTIME_IMAGE
FROM ${RUNTIME_IMAGE}
USER root
RUN install -d -o 1000 -g 1000 -m 0700 /home/runtime /environment /environment/workspace \
    && rmdir /workspace && ln -s /environment/workspace /workspace
USER 1000:1000
