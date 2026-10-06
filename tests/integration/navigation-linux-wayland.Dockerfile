# TEST-only extension of the independently qualified private portal image.
ARG PORTAL_TEST_IMAGE
FROM ${PORTAL_TEST_IMAGE}
ARG GTK_SOURCE_SHA256=47a3743d2419a8601e691db37e85bb5fac5ae4b26842177065cd5f22ada23b37
RUN apt-get update && \
    apt-get install -y --no-install-recommends sway=1.9-1build2 mako-notifier=1.8.0-2build2 \
      libgtk-3-dev libwayland-dev wayland-protocols && \
    dpkg-query -W > /fixture-build/wayland-packages.txt
COPY gtk-source.tar.xz wlr-virtual-pointer-unstable-v1.xml navigation_wayland_pointer_test.c /fixture-build/
RUN test "$GTK_SOURCE_SHA256" = 47a3743d2419a8601e691db37e85bb5fac5ae4b26842177065cd5f22ada23b37 && \
    echo "$GTK_SOURCE_SHA256  /fixture-build/gtk-source.tar.xz" | sha256sum -c - && \
    echo '3ff6d540be0bc5228195bf072bde42117ea17945a5c2061add5d3cf97d6bb524  /fixture-build/wlr-virtual-pointer-unstable-v1.xml' | sha256sum -c - && \
    mkdir /fixture-build/gtk-source && \
    tar -xJf /fixture-build/gtk-source.tar.xz --strip-components=1 -C /fixture-build/gtk-source && \
    export PKG_CONFIG_PATH=/opt/portal/share/pkgconfig && \
    test "$(pkg-config --modversion xdg-desktop-portal)" = 1.22.1 && \
    test "$(pkg-config --variable=interfaces_dir xdg-desktop-portal)" = /opt/portal/share/dbus-1/interfaces/ && \
    pkg-config --modversion xdg-desktop-portal > /fixture-build/gtk-portal-build-binding.txt && \
    pkg-config --variable=interfaces_dir xdg-desktop-portal >> /fixture-build/gtk-portal-build-binding.txt && \
    /opt/meson/bin/meson setup /fixture-build/gtk-build /fixture-build/gtk-source --prefix=/opt/gtk --libexecdir=libexec \
      -Dwallpaper=disabled -Dsettings=disabled -Dappchooser=disabled -Dlockdown=disabled && \
    /opt/meson/bin/meson compile -C /fixture-build/gtk-build && \
    /opt/meson/bin/meson install -C /fixture-build/gtk-build && \
    wayland-scanner client-header /fixture-build/wlr-virtual-pointer-unstable-v1.xml /fixture-build/wlr-virtual-pointer-client-protocol.h && \
    wayland-scanner private-code /fixture-build/wlr-virtual-pointer-unstable-v1.xml /fixture-build/wlr-virtual-pointer-protocol.c && \
    cc -std=c11 -Wall -Wextra -Werror -I/fixture-build /fixture-build/navigation_wayland_pointer_test.c \
      /fixture-build/wlr-virtual-pointer-protocol.c $(pkg-config --cflags --libs wayland-client) -o /fixture/navigation-wayland-pointer-test
COPY navigation-linux-wayland-token-probe.py /fixture/
ENTRYPOINT ["/usr/bin/python3", "/fixture/navigation-linux-wayland-token-probe.py"]
