#include <errno.h>
#include <stdio.h>
#include <fcntl.h>
#include <netdb.h>
#include <string.h>
#include <sys/param.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/utsname.h>
#include <time.h>
#include <unistd.h>
#include <ctype.h>

#include"util.h"

int mkpdir(const char *dirname, int mode)
{
	struct stat s;
	char buf[MAXPATHLEN];
	char *p;
	const char *q = dirname;

	if (stat(dirname, &s) < 0) {
		if (errno != ENOENT)
			return -1;
		p = buf;
		if (*q == '/')
			*p++ = *q++;
		while (*q) {
			while (*q && *q != '/')
				*p++ = *q++;
			*p = '\0';
			if (stat(buf, &s) < 0) {
				if (errno != ENOENT)
					return -1;
				if (mkdir(buf, mode) < 0)
					return -1;
			} else {
				if (!(S_ISDIR(s.st_mode))) {
					errno = ENOTDIR;
					return -1;
				}
			}
			while (*q == '/')
				*p++ = *q++;
		}
	}
	if (!(S_ISDIR(s.st_mode))) {
		errno = ENOTDIR;
		return -1;
	}
	return 0;
}

const char *group2dir(const char *name)
{
	static char buf[MAXPATHLEN];

	if (strlen(name) >= sizeof(buf)) {
		return NULL;
	}
	char *p = buf;
	for (const char *q = name; ; p++, q++) {
		*p = (*q != '.') ? *q : '/';
		if (*q == '\0')
			break;
	}

	return buf;
}

int matchgroup(const char *pattern, const char *group)
{
	char c;
	int j, clen, bmlen = 0, bmrej = 0;
	int rejrule = 0;

	j = 0;
	do {
		clen = 0;
		if (pattern[j] == '!') {
			j++;
			rejrule = 1;
		} else {
			rejrule = 0;
		}
		while ((c = pattern[j]) && c == group[clen]) {
			clen++;
			j++;
		}

		switch (c) {
		case ',':
		case '\0':
			if (group[clen] != '\0' && !isspace(group[clen])) {
				clen = -2;
			}
			clen++;
			break;
		case '*':
			clen++;
			break;
		default:
			clen = -1;
			break;
		}

		if (clen > bmlen) {
			bmlen = clen;
			bmrej = rejrule;
		}

		while (c && c != ',') {
			j++;
			c = pattern[j];
		}
		j++;
	} while (c);

	return bmrej ? -bmlen : bmlen;
}

const char *getfqdn(void)
{
	static char primary_hostname[FQDN_BUFSIZE];
	struct utsname uts;
	struct hostent *host;

	if (uname(&uts) < 0)
		return NULL;	// failed
	if (strchr(uts.nodename, '.') == NULL &&
	    (host = gethostbyname(uts.nodename)) != NULL) {
		snprintf(primary_hostname, sizeof(primary_hostname), "%s",
			 host->h_name);
	} else {
		snprintf(primary_hostname, sizeof(primary_hostname), "%s",
			 uts.nodename);
	}

	return primary_hostname;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
