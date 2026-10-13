# TEST-only QEMU tooling. No client, agent or VM starts during image build.
FROM ubuntu:24.04@sha256:f610ab94648195aa356059f5b41d6085c9d4d903c072430cdd1af7bdb646106b
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get -o APT::Update::Error-Mode=any update && mkdir -p /fixture-build && \
    apt-cache policy qemu-system-x86 qemu-utils cloud-image-utils genisoimage \
      python3 curl gnupg ca-certificates > /fixture-build/apt-policy.txt && \
    packages='' && \
    for package in qemu-system-x86 qemu-utils cloud-image-utils genisoimage python3 curl gnupg ca-certificates; do \
      version=$(apt-cache policy "$package" | awk '/Candidate:/ {print $2}'); \
      test -n "$version" && test "$version" != '(none)' || exit 1; \
      packages="$packages $package=$version"; \
    done && \
    apt-get install -y --no-install-recommends $packages && \
    dpkg-query -W > /fixture-build/packages.txt && \
    cp -a /etc/apt/sources.list.d /fixture-build/apt-sources && \
    /usr/bin/qemu-system-x86_64 --version > /fixture-build/qemu-version.txt && \
    /usr/bin/qemu-img --version > /fixture-build/qemu-img-version.txt && \
    mkdir /fixture
USER 1000:1000
WORKDIR /fixture
ENTRYPOINT ["/usr/bin/python3"]
