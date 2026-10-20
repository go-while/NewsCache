# NewsCache in a container (Docker or Podman, also Docker Desktop on Windows).
#
#   docker build -t newscache .
#   docker run -d --name newscache -p 119:119 -v newscache-spool:/var/cache/newscache newscache
#   docker logs -f newscache
#
# The default config (docker/newscache.conf) reads from
# news.blueworldhosting.com over TLS and lets clients from private networks
# read and post. Use your own config with
#   -v /path/to/newscache.conf:/etc/newscache.conf:ro
# Purge the spool now and then with: docker exec newscache newscacheclean

FROM docker.io/library/ubuntu:24.04 AS build
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential autoconf automake texinfo libgnutls28-dev \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY . .
# No syslog in a container: log to a file, which the image links to stderr.
RUN ./autogen \
 && ./configure --prefix=/usr --sysconfdir=/etc --localstatedir=/var \
      --without-syslog \
 && make -j"$(nproc)" \
 && make install DESTDIR=/out

FROM docker.io/library/ubuntu:24.04
# ca-certificates: the system trust store NewsCache verifies TLS servers with.
# netbase: /etc/services, for "ListenTo DEFAULT" and service names like nntp.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      libgnutls30t64 ca-certificates netbase \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /
COPY docker/newscache.conf /etc/newscache.conf
# The base image has the user and group news (uid/gid 9).
RUN install -d -o news -g news -m 755 /var/cache/newscache /var/log/newscache \
 && ln -s /dev/stderr /var/log/newscache/newscache.log \
 && ln -s /dev/stderr /var/log/newscache/newscache_newscacheclean.log \
 && newscache -c /etc/newscache.conf -p >/dev/null
VOLUME /var/cache/newscache
EXPOSE 119
# -n: stay in the foreground, as the container's main process.
CMD ["/usr/sbin/newscache", "-n", "-c", "/etc/newscache.conf"]
