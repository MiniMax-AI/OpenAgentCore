# The build context contains only oac-web and the existing Web dist.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e754765ad9e167b0677b41c617fd44afb7b9818a477f48f17bda08e12cfb98cb

COPY --chmod=0555 oac-web /usr/local/bin/oac-web
COPY dist /www
ENV OAC_WEB_ADDR=:8080 OAC_WEB_DIST=/www
EXPOSE 8080
USER 65532:65532
CMD ["/usr/local/bin/oac-web"]
