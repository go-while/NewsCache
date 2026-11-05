#include<stdlib.h>
#include<stdio.h>
#include<string.h>
#ifndef WITH_SYSLOG
#include<errno.h>
#include<fcntl.h>
#include<unistd.h>
#include<time.h>
#include<sys/stat.h>
#include<sys/time.h>
#endif

#include"Logger.h"

using namespace std;

#ifndef WITH_SYSLOG
static void write_all(int fd, const char *s)
{
	size_t len = strlen(s);

	while (fd >= 0 && len > 0) {
		ssize_t n = ::write(fd, s, len);
		if (n < 0) {
			if (errno == EINTR)
				continue;
			return;
		}
		s += n;
		len -= n;
	}
}
#endif

// private:
int Logger::bufresize(int sz)
{
	int nsz;

	/* Round up to the next 4k boundary */
	nsz = (sz + 0x1000) & ~0xfff;
	if (nsz != bufsz) {
		/* Add Space */
		if (buf)
			buf = (char *) realloc(buf, nsz);
		else
			buf = (char *) malloc(nsz);
		if (!buf) {
			bufsz = 0;
			return 0;
		}
		bufsz = nsz;
	}
	return bufsz;
}

void Logger::append(const char *s)
{
	int slen = strlen(s);
	if (bufresize(buflen + slen + 1) == 0)
		exit(26);
	strcat(buf, s);
	buflen += slen;
}

void Logger::print()
{
	char *nl, *p;

	while ((nl = strchr(buf, '\n')) != NULL) {
		// Print first line
		*nl = '\0';
		// A log line is one line of text. Client input in it (a
		// command, a user name, a group name, a host name from DNS)
		// can carry a lone CR, ESC sequences and other control
		// bytes, which change how the line looks on a terminal or
		// split it in a parser; they become '?'.
		for (p = buf; *p; p++)
			if ((unsigned char) *p < 0x20 || *p == 0x7f)
				*p = '?';
#ifdef WITH_SYSLOG
		syslog(_priority, "%s", buf);
#else
		time_t ts;
		struct tm tmbuf, *t;
		// room for seven ints, so snprintf can't truncate
		char tbuf[96];
		time(&ts);
		t = localtime_r(&ts, &tmbuf);
		if (t)
			snprintf(tbuf, sizeof(tbuf),
				 "%d %04d/%02d/%02d %02d:%02d:%02d ",
				 _priority, 1900 + t->tm_year, t->tm_mon + 1,
				 t->tm_mday, t->tm_hour, t->tm_min, t->tm_sec);
		else
			snprintf(tbuf, sizeof(tbuf), "%d ", _priority);
		// One write() per line: with O_APPEND, only a single write
		// keeps the lines of parallel sessions apart
		string line(tbuf);
		line += buf;
		line += '\n';
		write_all(logfd, line.c_str());
#endif

		// Move remaining string to the beginning
		nl++;
		p = buf;
		while ((*p++ = *nl++) != '\0');
	}
	buflen = strlen(buf);
}

// public:
#ifdef WITH_SYSLOG
Logger::Logger()
{
	buf = NULL;
	bufsz = 0;
	if (bufresize(1) == 0)
		exit(26);
	buf[0] = '\0';
	buflen = 0;
	_priority = Debug;
}

void Logger::open(const char *ident, int option, int facility)
{
	openlog(ident, option, facility);
}

Logger::~Logger()
{
	close();
	free(buf);
}

void Logger::close()
{
	closelog();
}

#else
Logger::Logger(const char *fn, int option)
{
	buf = NULL;
	bufsz = 0;
	if (bufresize(1) == 0)
		exit(26);
	buf[0] = '\0';
	buflen = 0;
	_priority = Debug;
	logfd = -1;

	open(fn);
}

Logger::~Logger()
{
	close();
	free(buf);
}

/*
 * The programs open their log as root, before setugid, and the directory
 * may be writable for Username: don't follow a symlink there or write into
 * a hard link (either could reach any file), only a regular file with one
 * link is used. A symlink to our own stderr, as in the container image,
 * means stderr. A new log file gets mode 0640: it names clients, users
 * and groups. An existing file keeps its mode.
 */
static int open_log(const char *fn, const char **err)
{
	struct stat st, est;
	int fd;

	*err = NULL;
	fd = ::open(fn, O_WRONLY | O_APPEND | O_CREAT | O_NOFOLLOW |
		    O_NOCTTY | O_NONBLOCK | O_CLOEXEC, 0640);
	if (fd < 0) {
		// ELOOP for a symlink, but EMLINK on FreeBSD
		int e = errno;
		if (lstat(fn, &st) == 0 && S_ISLNK(st.st_mode)) {
			if (stat(fn, &st) == 0 && fstat(STDERR_FILENO, &est) == 0 &&
			    st.st_dev == est.st_dev && st.st_ino == est.st_ino) {
				fd = fcntl(STDERR_FILENO, F_DUPFD_CLOEXEC, 3);
				if (fd >= 0)
					return fd;
				*err = strerror(errno);
			} else
				*err = "a symbolic link (only one to stderr is used)";
		} else
			*err = strerror(e);
	} else if (fstat(fd, &st) < 0)
		*err = strerror(errno);
	else if (!S_ISREG(st.st_mode) || st.st_nlink != 1)
		*err = "not a regular file with one link, left alone";
	if (*err && fd >= 0) {
		::close(fd);
		fd = -1;
	}
	return fd;
}

void Logger::open(const char *fn)
{
	const char *err;

	if (!fn)
		return;
	close();
	logname = fn;
	logfd = open_log(fn, &err);
	if (err)
		cerr << "cannot open log file " << fn << ": " << err << "\n";
}

/*
 * Log rotation renames the file (logrotate's default, "create"): the
 * process went on writing into the renamed one until it ended, so only
 * copytruncate worked, and lost lines. newscache calls this on SIGHUP.
 * As open(): the same checks, so the new file must be a regular file
 * with one link; the user the process runs as by then must be able to
 * open it, or create it in the directory.
 */
void Logger::reopen()
{
	const char *err;
	int fd;

	if (logname.empty())
		return;
	fd = open_log(logname.c_str(), &err);
	if (fd < 0) {
		string line("cannot reopen log file ");
		line += logname;
		line += ": ";
		line += err ? err : "unknown error";
		line += ", still writing to the old one\n";
		p(Error) << line;
		return;
	}
	close();
	logfd = fd;
}

void Logger::close()
{
	if (logfd >= 0)
		::close(logfd);
	logfd = -1;
}
#endif

Logger & Logger::priority(int p)
{
	// a line left without "\n" goes out at its own priority, before the
	// new one applies
	if (buflen && buf[buflen - 1] != '\n')
		write("\n");
	_priority = p;
	return *this;
}

Logger & Logger::write(const char *s)
{
	append(s);
	print();
	return *this;
}

// friends:
Logger & operator<<(Logger & l, const char *s)
{
	return l.write(s);
}

Logger & operator<<(Logger & l, const string & s)
{
	string s2(s);
	return l.write(s2.c_str());
}

Logger & operator<<(Logger & l, char ch)
{
	return l.write(ch);
}

Logger & operator<<(Logger & l, unsigned int i)
{
	char s[256];
	sprintf(s, "%u", i);
	return l.write(s);
}

Logger & operator<<(Logger & l, int i)
{
	char s[256];
	sprintf(s, "%d", i);
	return l.write(s);
}

// For article numbers (artnum_t is one of the unsigned ones) and sizes
Logger & operator<<(Logger & l, unsigned long i)
{
	char s[32];
	snprintf(s, sizeof(s), "%lu", i);
	return l.write(s);
}

Logger & operator<<(Logger & l, long i)
{
	char s[32];
	snprintf(s, sizeof(s), "%ld", i);
	return l.write(s);
}

Logger & operator<<(Logger & l, unsigned long long i)
{
	char s[32];
	snprintf(s, sizeof(s), "%llu", i);
	return l.write(s);
}

Logger & operator<<(Logger & l, long long i)
{
	char s[32];
	snprintf(s, sizeof(s), "%lld", i);
	return l.write(s);
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
