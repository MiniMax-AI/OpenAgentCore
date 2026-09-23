# The build context contains only core-console and the existing Web dist.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e754765ad9e167b0677b41c617fd44afb7b9818a477f48f17bda08e12cfb98cb

COPY --chmod=0555 core-console /usr/local/bin/core-console
COPY dist /www
ENV CORE_CONSOLE_ADDR=:8080 CORE_CONSOLE_DIST=/www
EXPOSE 8080
USER 65532:65532
CMD ["/usr/local/bin/core-console"]
