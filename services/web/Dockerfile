# The build context contains only oac-web and the existing Web dist.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

COPY --chmod=0555 oac-web /usr/local/bin/oac-web
COPY dist /www
EXPOSE 8080
USER 65532:65532
CMD ["/usr/local/bin/oac-web"]
