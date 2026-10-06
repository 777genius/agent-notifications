# TEST-only image. Source archive is prepared by the primary actor using gh CLI.
FROM ubuntu:24.04@sha256:f610ab94648195aa356059f5b41d6085c9d4d903c072430cdd1af7bdb646106b
ARG DEBIAN_FRONTEND=noninteractive
ARG PORTAL_SOURCE_SHA256
ARG PORTAL_SOURCE_COMMIT=1d20fadc304f6601452b5db65ed91197dba77041
RUN apt-get update && mkdir -p /fixture-build && \
    gtk=$(apt-cache policy xdg-desktop-portal-gtk | awk '/Candidate:/ {print $2}') && \
    dunst=$(apt-cache policy dunst | awk '/Candidate:/ {print $2}') && \
    case "$gtk" in 1.15.1-*) ;; *) echo 'Noble GTK backend version mismatch'; exit 1;; esac && \
    test "$dunst" != '(none)' && \
    printf 'GTK=%s\nDUNST=%s\n' "$gtk" "$dunst" > /fixture-build/apt-candidates.txt && \
    apt-get install -y --no-install-recommends "xdg-desktop-portal-gtk=$gtk" "dunst=$dunst" \
      python3 python3-gi python3-pip python3-venv dbus dbus-x11 xvfb xdotool x11-utils imagemagick \
      fonts-dejavu-core ninja-build build-essential pkg-config gettext libglib2.0-dev libjson-glib-dev \
      libfuse3-dev libgdk-pixbuf-2.0-dev libgstreamer-plugins-base1.0-dev libpipewire-0.3-dev \
      libsystemd-dev libgudev-1.0-dev ca-certificates xz-utils && \
    dpkg-query -W > /fixture-build/packages.txt && python3 -m venv /opt/meson && \
    /opt/meson/bin/pip install --no-cache-dir meson==1.12.1
COPY portal-source.tar.xz /fixture-build/portal-source.tar.xz
RUN test "$PORTAL_SOURCE_COMMIT" = 1d20fadc304f6601452b5db65ed91197dba77041 && \
    test -n "$PORTAL_SOURCE_SHA256" && echo "$PORTAL_SOURCE_SHA256  /fixture-build/portal-source.tar.xz" | sha256sum -c - && \
    mkdir /fixture-build/source && tar -xJf /fixture-build/portal-source.tar.xz --strip-components=1 -C /fixture-build/source && \
    grep -q "version: '1.22.1'" /fixture-build/source/meson.build && \
    /opt/meson/bin/meson setup /fixture-build/build /fixture-build/source --prefix=/opt/portal \
      --libexecdir=libexec -Dtests=disabled -Ddocumentation=disabled -Dman-pages=disabled \
      -Dflatpak-interfaces=disabled -Dgeoclue=disabled -Dsandboxed-image-validation=disabled -Dsandboxed-sound-validation=disabled && \
    /opt/meson/bin/meson compile -C /fixture-build/build && /opt/meson/bin/meson install -C /fixture-build/build && \
    /opt/portal/libexec/xdg-desktop-portal --version > /fixture-build/portal-version.txt && \
    /opt/meson/bin/meson --version > /fixture-build/meson-version.txt
# Separate late runtime layer preserves the accepted frontend/Meson build cache.
RUN apt-get update && xres=$(apt-cache policy libxres1 | awk '/Candidate:/ {print $2}') && \
    test -n "$xres" && test "$xres" != '(none)' && \
    apt-get install -y --no-install-recommends "libxres1=$xres" && \
    printf 'XRES=%s\n' "$xres" >> /fixture-build/apt-candidates.txt && \
    dpkg-query -W > /fixture-build/packages.txt
COPY navigation-linux-portal-callback-probe.py navigation_linux_portal_test_app.py /fixture/
ENTRYPOINT ["/usr/bin/python3", "/fixture/navigation-linux-portal-callback-probe.py", "--inside-test-container"]
