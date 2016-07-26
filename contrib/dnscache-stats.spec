# Go binaries should not be stripped
%global __strip /bin/true

Name: dnscache-stats
Version: 0.0.1
Release: 1
License: MIT
Group: Development/Tools
URL: https://github.com/droyo/dnscache-stats
BuildRoot: %{_buildroot}
Summary: Generate metrics from dnscache logs

%description
The dnscache-stats program can be used to send metrics generated from the log
entries of the dnscache program to a graphite-compatible metrics database.

%install
rm -rf $RPM_BUILD_ROOT
mkdir -p $RPM_BUILD_ROOT%{_bindir}
mkdir -p $RPM_BUILD_ROOT%{_mandir}/man8
install -m755 dnscache-stats $RPM_BUILD_ROOT%{_bindir}/dnscache-stats
install -m755 dnscache-stats.8 $RPM_BUILD_ROOT%{_mandir}/man8/dnscache-stats.8

%files
%defattr(-, root, root, -)
%{_bindir}/dnscache-stats

%changelog
* Tue Jul 26 2016 David Arroyo <droyo@aqwari.net>
- Initial build
