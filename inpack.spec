[project]
name = ssdb
version = 1.9.4
vendor = ssdb.io
homepage = http://ssdb.io
groups = dev/db
description = A high performance NoSQL database supporting many data structures, an alternative to Redis

%build

cd {{.inpack__pack_dir}}/deps

if [ ! -f "{{.project__version}}.tar.gz" ]; then
    wget https://github.com/ideawu/ssdb/archive/{{.project__version}}.tar.gz
fi
if [ -d "ssdb-{{.project__version}}" ]; then
    rm -rf ssdb-{{.project__version}}
fi
tar -zxf {{.project__version}}.tar.gz

cd ssdb-{{.project__version}}

make -j4

rm -rf   {{.buildroot}}/*
mkdir -p {{.buildroot}}/{bin,etc,var}

install ssdb-server             {{.buildroot}}/bin/ssdb-server
# strip -s {{.buildroot}}/bin/ssdb-server

cd {{.inpack__pack_dir}}

install misc/ssdb.conf.default      {{.buildroot}}/etc/ssdb.conf.default

rm -rf {{.inpack__pack_dir}}/deps/ssdb-{{.project__version}}

%files

