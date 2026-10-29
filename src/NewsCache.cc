/* NewsCache.cc
 * -- (c) 1996-1998 by Thomas GSCHWIND <tom@infosys.tuwien.ac.at>
 * -- implements the cache server in combination with the NServer library
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 2 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, see <https://www.gnu.org/licenses/>.
 */
/*
 * Portions Copyright (C) 2001-2003 Herbert Straub
 * Herbert Straub: modify ns_stat: bug fix stat by messageId
 * Herbert Straub: adding nice values for master and client
 * Herbert Straub: debug output in selectgroup
 */
#include "config.h"

#ifdef HAVE_CRYPT_H
#include <crypt.h>	/* elsewhere (BSD, macOS) crypt() is in unistd.h */
#endif
#include <ctype.h>
#include <unistd.h>
#include <string.h>
#include <errno.h>
#ifdef HAVE_GETOPT_H
#include <getopt.h>
#endif
#include <netinet/in.h>
#include <arpa/inet.h>
#include <netdb.h>
#include <signal.h>
#include <sys/socket.h>
#ifdef HAVE_SYS_TIME_H
#include <sys/time.h>
#endif
#ifdef HAVE_SETPRIORITY
#include <sys/resource.h>
#endif
#include <sys/types.h>
#include <sys/wait.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <poll.h>
#include <time.h>
#include <limits.h>

const char * const ConfigurationOptions[] = {
#ifdef MD5_CRYPT
	"--with-md5",
#endif
#ifdef MD5_AUTO
	"--with-md5auto",
#endif
#ifdef SHADOW_PASSWORD
	"--with-shadow",
#endif
#ifdef PAM_AUTH
	"--with-pam=" PAM_DEFAULT_SERVICENAME,
#endif
#ifdef WITH_SYSLOG
	"--with-syslog",
#endif
#ifdef ENABLE_HEADERLOG
	"--enable-headerlog",
#endif
#ifdef ENABLE_DEBUG
	"--enable-debug",
#endif
#ifdef PLAIN_TEXT_PASSWORDS
	"--enable-plainpass",
#endif
#ifdef ENABLE_ASSERTIONS
	"--enable-assertions",
#endif
#ifdef WITH_UNIQUE_PACKAGE_NAME
	"--with-uniquename",
#endif
#ifdef HAVE_GNUTLS
	"--with-gnutls",
#endif
	NULL
};

#ifdef HAVE_LIBWRAP
#include <tcpd.h>
extern "C" int hosts_ctl(char *daemon, char *client_name,
			 char *client_addr, char *client_user);
#endif

#if (defined (MD5_AUTO) && !defined (MD5_CRYPT))
#define MD5_CRYPT
#endif				/*MD5_AUTO */

#include <unistd.h>
#include <pwd.h>
#ifdef SHADOW_PASSWORD
#include <shadow.h>
#endif				/*SHADOW_PASSWORD */
#ifdef MD5_CRYPT
#include "md5crypt.h"
#endif				/*MD5_CRYPT */
#ifdef PAM_AUTH			/*PAM_AUTH */
#include <security/pam_appl.h>
#include <grp.h>
#endif				/*PAM_AUTH */

#include <iostream>
#include <algorithm>
#include <limits>
#include <map>
#include <set>
#include <sstream>
#include <string>
#include <vector>

#include "NServer.h"
#include "NVNewsgroup.h"
#include "History.h"
#include "Newsgroup.h"

#include "util.h"
#include "setugid.h"
#include "NewsCacheConfig.h"
#include "Logger.h"
#include "sockstream.h"

using std::cin;
using std::cout;
using std::cerr;
using std::endl;
using std::for_each;
using std::ifstream;
using std::ios;
using std::istream;
using std::ofstream;
using std::ostream;
using std::ostringstream;
using std::map;
using std::set;
using std::string;
using std::vector;

/* CONF_MultiClient
 * Allow several simultaneous clients in standalone mode.
 * This switch is turned off for debug purposes mainly.
 */
#define CONF_MultiClient

//FIXME: since porting to socket++, this had to connent out
/* If you are very performance concerned, you might try to comment this
#define NO_STREAM_BUFFERING
 */

#ifndef SOCKLEN_TYPE
#define SOCKLEN_TYPE size_t
#endif

sig_atomic_t alarmed=0;
Logger slog;
const char *cmnd;
class ClientData;
// nntp helper functions
int ns_authinfo(ClientData * clt, int argc, char *argv[]);
int ns_article(ClientData * clt, int argc, char *argv[]);
int ns_stat(ClientData * clt, int argc, char *argv[]);
int ns_date(ClientData * clt, int argc, char *argv[]);
int ns_group(ClientData * clt, int argc, char *argv[]);
int ns_help(ClientData * clt, int argc, char *argv[]);
int ns_lastnext(ClientData * clt, int argc, char *argv[]);
int ns_list(ClientData * clt, int argc, char *argv[]);
int ns_listgroup(ClientData * clt, int argc, char *argv[]);
int ns_mode(ClientData * clt, int argc, char *argv[]);
int ns_newgroups(ClientData * clt, int argc, char *argv[]);
int ns_post(ClientData * clt, int argc, char *argv[]);
int ns_quit(ClientData * clt, int argc, char *argv[]);
int ns_xover(ClientData * clt, int argc, char *argv[]);
int ns_xpat(ClientData * clt, int argc, char *argv[]);
int ns_hdr(ClientData * clt, int argc, char *argv[]);
int ns_xdebug(ClientData * clt, int argc, char *argv[]);
int ns_capabilities(ClientData * clt, int argc, char *argv[]);

typedef struct nnrp_command_t {
	const char *name;
	const char *desc;
	int (*func) (ClientData *, int, char *[]);
} nnrp_command_t;

static nnrp_command_t all_nnrp_commands[] = {
	{"authinfo", "[user username|pass password]", ns_authinfo}
	,
	{"article", "[number|<id>]", ns_article}
	,
	{"head", "[number|<id>]", ns_article}
	,
	{"body", "[number|<id>]", ns_article}
	,
	{"stat", "[number|<id>]", ns_stat}
	,
	{"date", "", ns_date}
	,
	{"group", "newsgroup", ns_group}
	,
	{"hdr", "field [range|<id>]", ns_hdr}
	,
	{"help", "", ns_help}
	,
	{"last", "", ns_lastnext}
	,
	{"list",
	 "[active [wildmat]|active.times [time]|extensions|headers [msgid|range]|newsgroups [wildmat]|overview.fmt]",
	 ns_list}
	,
	{"listgroup", "[newsgroup [range]]", ns_listgroup}
	,
	{"mode", "reader", ns_mode}
	,
	{"newgroups", "date time [GMT] [wildmat]", ns_newgroups}
	,
	{"next", "", ns_lastnext}
	,
	{"over", "[range]", ns_xover}
	,
	{"post", "", ns_post}
	,
	{"quit", "", ns_quit}
	,
	{"xdebug", "help", ns_xdebug}
	,
	{"xhdr", "header [range|<id>]", ns_xover}
	,
	{"xover", "[range]", ns_xover}
	,
	{"xpat", "header range|<id> pattern [pattern ...]", ns_xpat}
	,
	{"capabilities", "", ns_capabilities}
	,

	{NULL, NULL, NULL}
};


/*
 * \class NNRPCommandMap
 * \author Thomas Gschwind, Herbert Straub
 * \brief manage the commands
 */
class NNRPCommandMap:public map < string, nnrp_command_t * > {
      public:
	 /*
	  * Constructor enable all commands.
	  */
	 NNRPCommandMap();

	 /*
	  * Enable all commands.
	  */
	 void enableAll (void);

	 /*
	  * Disable reader commands
	  * \author Herbert Straub
	  */
	 void disableRead (void);

	 /*
	  * Disable Post command.
	  * \author Herbert Straub
	  */
	 void disablePost (void);

	 /*
	  * Disable Debug command.
	  * \author Herbert Straub
	  */
	 void disableDebug (void);
};
void set_client_command_table (ClientData &clt);

void NNRPCommandMap::enableAll (void)
{
	nnrp_command_t *p = all_nnrp_commands;
	while (p->name) {
		(*this)[p->name] = p;
		p++;
	}
}

NNRPCommandMap::NNRPCommandMap ()
{
	enableAll();
}

void NNRPCommandMap::disableRead (void)
{
	erase("article");
	erase("head");
	erase("body");
	erase("stat");
	erase("group");
	erase("last");
	erase("listgroup");
	erase("next");
	erase("over");
	erase("xhdr");
	erase("hdr");
	erase("xover");
	erase("xpat");
	erase("list");
	erase("newgroups");
}

void NNRPCommandMap::disablePost (void)
{
	erase("post");
}

void NNRPCommandMap::disableDebug (void)
{
	erase("xdebug");
}

static NNRPCommandMap nnrp_commands;

char config_file[MAXPATHLEN];
Config Cfg;
volatile sig_atomic_t reReadConfig;

volatile sig_atomic_t Xsignal;
volatile sig_atomic_t nntp_connections;	// sigchld() decrements it

// The master wakes poll() through this nonblocking pipe. A signal arriving
// after the flag check but before poll must not leave an idle daemon asleep.
// Session children disable and close their inherited pipe ends after fork.
static volatile sig_atomic_t signal_wake_write = -1;

static void wake_master(void)
{
	int saved_errno = errno;
	if (signal_wake_write >= 0) {
		const char byte = 0;
		// EAGAIN is harmless: a full pipe already wakes the poller.
		ssize_t n;
		do {
			n = write((int) signal_wake_write, &byte, 1);
		} while (n < 0 && errno == EINTR);
	}
	errno = saved_errno;
}

/* The daemon's session processes, so that it can end them when it stops
 * (they kept running until ClientTimeout). Only sigchld() changes a pid
 * to 0; the main flow touches the table with SIGCHLD blocked. */
static vector < pid_t > sessions;

static void block_sigchld(sigset_t * old)
{
	sigset_t set;
	sigemptyset(&set);
	sigaddset(&set, SIGCHLD);
	sigprocmask(SIG_BLOCK, &set, old);
}

// Fork with termination and reload signals blocked too, so the child can
// clear the master's state and close the wake pipe before handling signals.
static void block_fork_signals(sigset_t * old)
{
	sigset_t set;
	sigemptyset(&set);
	sigaddset(&set, SIGCHLD);
	sigaddset(&set, SIGINT);
	sigaddset(&set, SIGTERM);
	sigaddset(&set, SIGHUP);
	sigprocmask(SIG_BLOCK, &set, old);
}

static void add_session(pid_t pid)
{
	size_t i;
	for (i = 0; i < sessions.size() && sessions[i] != 0; i++)
		;
	if (i < sessions.size())
		sessions[i] = pid;
	else
		sessions.push_back(pid);
}

static void end_sessions(void)
{
	sigset_t old;
	block_sigchld(&old);
	for (size_t i = 0; i < sessions.size(); i++)
		if (sessions[i] != 0)
			kill(sessions[i], SIGTERM);
}

/* Sessions that ended with an error, recorded by sigchld() and logged by
 * log_ended_sessions() in the main flow: the Logger isn't
 * async-signal-safe (malloc, localtime_r, stdio, syslog), and logging
 * from the handler deadlocked the daemon when it interrupted a log line. */
#define ENDED_MAX 64
static pid_t ended_pid[ENDED_MAX];
static int ended_status[ENDED_MAX];
static volatile sig_atomic_t ended_n, ended_lost;

static void log_ended_sessions(void)
{
	pid_t pid[ENDED_MAX];
	int st[ENDED_MAX];
	int n, lost;
	sigset_t old;

	if (ended_n == 0 && ended_lost == 0)
		return;
	block_sigchld(&old);
	n = ended_n;
	lost = ended_lost;
	memcpy(pid, ended_pid, n * sizeof(pid[0]));
	memcpy(st, ended_status, n * sizeof(st[0]));
	ended_n = 0;
	ended_lost = 0;
	sigprocmask(SIG_SETMASK, &old, NULL);

	for (int i = 0; i < n; i++) {
		if (WIFEXITED(st[i]))
			slog.p(Logger::Error) << pid[i] << " returned "
			    << WEXITSTATUS(st[i]) << "\n";
		else
			slog.p(Logger::Error) << pid[i] << " caught signal "
			    << WTERMSIG(st[i]) << "\n";
	}
	if (lost)
		slog.p(Logger::Error) << lost << " more sessions ended "
		    "with an error\n";
}

/* The pid file this daemon wrote, kept open so it can be emptied when the
 * daemon can't remove it any more (see remove_pid_file). */
static char pid_file[MAXPATHLEN];
static int pid_file_fd = -1;

/*
 * Writes our pid to Cfg.PidFile, unless PidFile is empty. Runs before
 * setugid, so a root-only directory such as /run works. As root, it must
 * not follow a symlink or write into a hard link that Username may have
 * put there (the directory can be writable for Username, see
 * newscache.conf(5)): only a regular file with one link is written.
 */
static void write_pid_file(void)
{
	char buf[32];
	struct stat st;
	const char *err = NULL;
	int len;

	if (Cfg.PidFile[0] == '\0')
		return;
	pid_file_fd = open(Cfg.PidFile, O_RDWR | O_CREAT | O_NOFOLLOW |
			   O_NONBLOCK | O_CLOEXEC, 0644);
	len = snprintf(buf, sizeof(buf), "%ld\n", (long) getpid());
	if (pid_file_fd < 0)
		err = strerror(errno);
	else if (fstat(pid_file_fd, &st) < 0)
		err = strerror(errno);
	else if (!S_ISREG(st.st_mode) || st.st_nlink != 1)
		err = "not a regular file with one link, left alone";
	else if (ftruncate(pid_file_fd, 0) < 0 ||
		 write(pid_file_fd, buf, len) != len)
		err = strerror(errno);
	if (err) {
		slog.p(Logger::Warning) << "cannot write pid file "
		    << Cfg.PidFile << ": " << err << "\n";
		if (pid_file_fd >= 0)
			close(pid_file_fd);
		pid_file_fd = -1;
		return;
	}
	snprintf(pid_file, sizeof(pid_file), "%s", Cfg.PidFile);
}

/*
 * Removes the pid file when the daemon stops, if it is still ours (same
 * file, our pid). After setugid, removing it needs a directory writable for
 * Username; where that fails (PidFile /run/newscache.pid), the file is
 * emptied, so no stale pid stays in it.
 */
static void remove_pid_file(void)
{
	struct stat fst, pst;
	char buf[32], own[32];
	ssize_t n;

	if (pid_file_fd < 0)
		return;
	snprintf(own, sizeof(own), "%ld\n", (long) getpid());
	n = pread(pid_file_fd, buf, sizeof(buf) - 1, 0);
	if (n < 0)
		n = 0;
	buf[n] = '\0';
	if (fstat(pid_file_fd, &fst) == 0 && stat(pid_file, &pst) == 0 &&
	    fst.st_dev == pst.st_dev && fst.st_ino == pst.st_ino &&
	    strcmp(buf, own) == 0) {
		if (unlink(pid_file) < 0) {
			int err = errno;
			if (ftruncate(pid_file_fd, 0) == 0)
				slog.p(Logger::Notice) << "emptied pid file "
				    << pid_file << ", cannot remove it: "
				    << strerror(err) << "\n";
			else
				slog.p(Logger::Warning) << "cannot remove pid file "
				    << pid_file << ": " << strerror(err) << "\n";
		}
	}
	close(pid_file_fd);
	pid_file_fd = -1;
}

enum {
	AUTH_REQUIRED,
	AUTH_OK,
	AUTH_FAILED,
	AUTH_NOT_REQUIRED
};

/**
 * \class ClientData
 * \author Thomas Gschwind
 *
 * \bug Documentation is missing.
 */
class ClientData {
  public:
	struct sockaddr_storage sock;
	SOCKLEN_TYPE socklen;
	// a DNS name has up to 253 characters (MAXHOSTNAMELEN is 64 on
	// Linux, and getnameinfo failed for longer names)
	char client_name[FQDN_BUFSIZE];
	char client_addr[INET6_ADDRSTRLEN];
	string client_logname;
	istream *ci;
	ostream *co;

	CServer *srvr;
	char groupname[MAXGROUPNAMELEN];
	Newsgroup *grp;
	artnum_t nbr;		// current article, 0 = none

	AccessEntry *access_entry;
	bool tls;		// the connection is TLS (TLSListenTo, -t)
	char auth_user[512];
	bool auth_user_valid; // a USER is pending for one PASS
	bool auth_refused;    // the last AUTHINFO checked a password and refused it
	char auth_pass[512];
	int auth_state;
	int auth_failures;
	int auth_max_failures;
	int auth_failure_sleep_time;

#ifdef PAM_AUTH
	pam_handle_t *pamh;
	struct pam_conv conv;
#endif

	int stat_groups;
	int stat_artingrp;
	int stat_articles;

	NNRPCommandMap client_command_map;

	ClientData()
		: client_logname(""),
		  grp(NULL), nbr(0),
		  access_entry(NULL), tls(false),
		  auth_user_valid(false), auth_refused(false),
		  auth_state(AUTH_OK),
		  auth_failures(0),
		  auth_max_failures(3), auth_failure_sleep_time(Cfg.AuthFailureDelay),
		  stat_groups(0), stat_artingrp(0), stat_articles(0)
	{
		auth_user[0] = auth_pass[0] = '\0';
		groupname[0] = '\0';
		// not set in inetd mode or when getpeername fails; PAM_RHOST
		// reads it
		client_addr[0] = '\0';
		// no address (AF_UNSPEC) for inetd mode and when getpeername
		// fails: the access list's address entries read it
		memset(&sock, 0, sizeof(sock));
		socklen = 0;
	}
};

#ifdef PAM_AUTH
/* pam_conv_func by Tuomo Pyhala <tuomo.pyhala@iki.fi> */
/* Dirty kludge */
int pam_conv_func(int num_msg, const struct pam_message **msg, struct pam_response **resp, void *appdata_ptr)
{
  int i;
  struct pam_response *resp_array = (struct pam_response*) calloc(num_msg, sizeof(struct pam_response));
  class ClientData *clnt = (ClientData *) appdata_ptr;

  slog.p(Logger::Debug) << "in pam_conf_func\n";
  if(resp_array == NULL) {
    return PAM_CONV_ERR; /* Out of memory */
  }
  for(i=0; i<num_msg; i++) {
    resp_array[i].resp_retcode = 0;
    resp_array[i].resp = NULL;
    switch(msg[i]->msg_style) {
    case PAM_PROMPT_ECHO_OFF: /* Assume this is password */
      resp_array[i].resp = strdup(clnt->auth_pass);
      break;
    case PAM_PROMPT_ECHO_ON: /* Assume this is username */
      resp_array[i].resp = strdup(clnt->auth_user);
      break;
    case PAM_ERROR_MSG:
      break;
    case PAM_TEXT_INFO:
      break;
    default:
      /* Error of some sort */
      free(resp_array);
      return PAM_CONV_ERR;
    }
  }
  *resp=resp_array;
  return PAM_SUCCESS;
}
#endif /* PAM_AUTH */

// Run in the server parent at startup and after a successful reload, not in
// seekfile(): the parent forks one child per connection, so child-local state
// would warn once per login rather than once per configured path.
static void warn_unsafe_auth_files(const AccessList &access_list)
{
	set<string> checked;
	const vector<AccessEntry> &entries = access_list.entries();
	for (vector<AccessEntry>::const_iterator i = entries.begin();
	     i != entries.end(); ++i) {
		const string &type = i->authentication.getType();
		if (type != "file" && type != "pam+file")
			continue;
		const string &filename = i->authentication.getField(1);
		if (filename.empty() || checked.find(filename) != checked.end())
			continue;
		struct stat st;
		if (lstat(filename.c_str(), &st) != 0)
			continue;
		checked.insert(filename);

		string reason;
		if (S_ISLNK(st.st_mode))
			reason = "is a symbolic link";
		else {
			bool group_writable = (st.st_mode & S_IWGRP) != 0;
			bool world_writable = (st.st_mode & S_IWOTH) != 0;
			if (group_writable && world_writable)
				reason = "is writable by group and world";
			else if (group_writable)
				reason = "is group-writable";
			else if (world_writable)
				reason = "is world-writable";
		}
		if (!reason.empty())
			slog.p(Logger::Warning) << "authentication file " << filename << " "
				<< reason << "\n";
	}
}

char **seekfile(const char *filename, const char *username)
{
	FILE *F;
	// Lines of any length: fgets() into 1024 bytes split a longer
	// line silently, and its rest was parsed as a line of its own.
	static char *buf = NULL;
	static size_t bufsize = 0;
	char *p;
	int i;

	static char *fields[8];
	int fields_n = sizeof(fields) / sizeof(fields[0]);

	if ((F = fopen(filename, "r")) == NULL) {
		slog.
		    p(Logger::
		      Error) << "cannot fopen " << filename << "\n";
		return NULL;
	}

	while (getline(&buf, &bufsize, F) != -1) {
		p = buf + strlen(buf) - 1;
		while (p > buf && isspace(*p))
			*p-- = '\0';
		p = buf;
		while (isspace(*p))
			p++;
		if (*p == '#' || *p == '\0')
			continue;

		fields[0] = p;
		for (i = 1; *p && i < fields_n - 1; p++) {
			if (*p == ':') {
				*p = '\0';
				fields[i++] = p + 1;
			}
		}
		fields[i] = NULL;

		if (strcmp(username, fields[0]) == 0) {
			(void) fclose(F);
			return fields;
		}
	}
	(void) fclose(F);
	return NULL;
}

/*
 * Whether two hashes (or passwords) differ, in time that depends on their
 * lengths only, not on where they differ: a byte-by-byte compare that
 * stops at the first difference tells how many leading characters of a
 * guess were right.
 */
static int hashes_differ(const char *a, const char *b)
{
	size_t la = strlen(a), lb = strlen(b);
	// With different lengths compare a with itself, so the loop does
	// the same work; the length difference alone decides then.
	const char *q = la == lb ? b : a;
	unsigned char diff = la != lb;

	for (size_t i = 0; i < la; i++)
		diff |= (unsigned char) a[i] ^ (unsigned char) q[i];
	return diff != 0;
}

/*
 * Checks the password key against the stored hash pass: 0 if it matches.
 * pass is an MD5 hash of ours (base64, 24 characters ending in "==") or a
 * hash for the system's crypt(), which reads the method and the salt from
 * it. Any other value refuses the login: crypt() returns NULL for a
 * setting it can't use (glibc, BSD: errno EINVAL, or ENOSYS for a method
 * that was compiled out), or a failure token "*0" or "*1" (libxcrypt,
 * musl). The NULL went into strcmp and killed the session; a stored
 * "*0" would have matched the token for any password. "*" and "!" are
 * how shadow files mark a locked account, so a hash starting with either
 * is refused outright.
 */
int auto_cryptcheck(const char *key, const char *pass)
{
	if (pass[0] == '\0' || pass[0] == '*' || pass[0] == '!') {
		slog.p(Logger::Error)
		    << "authinfo: the stored password hash is empty or marks a"
		    " locked account; the login is refused\n";
		return -1;
	}

	bool md5 = false;
#ifdef MD5_CRYPT
#ifdef MD5_AUTO
	// looks like an MD5 hash
	md5 = strlen(pass) == 24 && pass[23] == '=' && pass[22] == '=';
#else
	md5 = true;
#endif
#endif
	char *md5hash = NULL;
	const char *hash;
	if (md5) {
#ifdef MD5_CRYPT
		md5hash = md5crypt(key);	// malloc'ed, NULL if that failed
#endif
		hash = md5hash;
	} else {
		errno = 0;
		hash = crypt(key, pass);
	}

	int r;
	if (hash == NULL) {
		std::string why = md5 ? "cannot hash the password to check it"
		    : std::string("crypt() can't use the stored hash (")
		    + strerror(errno) + ")";
		slog.p(Logger::Error) << "authinfo: " << why
		    << "; the login is refused\n";
		r = -1;
	} else if (hash[0] == '*') {
		slog.p(Logger::Error)
		    << "authinfo: crypt() can't use the stored hash; the login"
		    " is refused\n";
		r = -1;
	} else {
		r = hashes_differ(pass, hash) ? -1 : 0;
	}
	free(md5hash);
	return r;
}

#ifdef PLAIN_TEXT_PASSWORDS
/*
 * Whether an auth-file password field may be compared as plain text: not
 * when it is a hash ($id$..., 13 characters of DES crypt, or the 24 of an
 * MD5 hash ending in "=="), nor a locked or empty field ("*", "!...").
 * Otherwise a locked line accepted the password "*", and any hash worked
 * as its own password.
 */
static bool plain_password_field(const char *f)
{
	size_t n = strlen(f), i;

	if (n == 0 || f[0] == '*' || f[0] == '!' || f[0] == '$')
		return false;
	if (n == 24 && f[22] == '=' && f[23] == '=')
		return false;
	if (n == 13) {
		for (i = 0; i < n; i++)
			if (!isalnum((unsigned char) f[i]) && f[i] != '.'
			    && f[i] != '/')
				break;
		if (i == n)
			return false;
	}
	return true;
}
#endif

int check_auth_unix (ClientData *clt)
{
	AccessEntry *access_entry = clt->access_entry;
	string type("unix:");

	if (access_entry->authentication.typeEqual (type))
		return -1;

	const char *pass;
	struct passwd *pw = NULL;
	pw = getpwnam(clt->auth_user);
	if (!pw || !(pw->pw_name) || !(pw->pw_passwd)
	    || !(pw->pw_passwd[0]))
		return -1;
	pass = pw->pw_passwd;
	if ((pass[0] == 'x') && (pass[1] == '\0')) {
#ifdef SHADOW_PASSWORD
		struct spwd *spw = NULL;
		errno = 0;
		spw = getspnam(pw->pw_name);
		if (!(spw) || !(spw->sp_namp) || !(spw->sp_pwdp)) {
			// Sessions run as Username (news), and only root may
			// read /etc/shadow; say so instead of failing silently.
			if (errno == EACCES || geteuid() != 0)
				slog.p(Logger::Error)
				    << "Authentication unix: cannot read the shadow password of "
				    << clt->auth_user << ": only root may read /etc/shadow, and"
				    " NewsCache runs as uid " << (unsigned long) geteuid()
				    << ". Use Authentication file: instead\n";
			return -1;
		}
		pass = spw->sp_pwdp;
#else
		slog.p(Logger::Error)
		    << "Authentication unix: " << clt->auth_user
		    << " has a shadow password, and NewsCache was built without"
		    " shadow password support\n";
		return -1;
#endif
	}
	if (auto_cryptcheck(clt->auth_pass, pass))
		return -1;

	access_entry->list |= NewsgroupFilter(
			(access_entry->authentication.getField(1))
			.c_str());
	access_entry->read |= NewsgroupFilter(
			(access_entry->authentication.getField(2))
			.c_str());
	access_entry->postTo |= NewsgroupFilter(
			(access_entry->authentication.getField(3)).
			c_str());
	access_entry->modifyAccessFlags (access_entry->authentication.
			getField(4));
	slog.p(Logger::Debug) << "check_auth_unix::NewsgroupFilter set to: "
		<< access_entry->list.getRulelist() << ":"
		<< access_entry->read.getRulelist() << ":"
		<< access_entry->postTo.getRulelist() << "\n";

	return 0;
}

/*
 * Adds the rights of an auth-file line to the entry. fields points to the
 * List field; then come Read, PostTo and the allow flags. The array ends
 * with NULL, and missing fields add nothing.
 */
static void add_auth_line_rights(AccessEntry *access_entry, char **fields)
{
	const char *f[4] = { "", "", "", "" };

	for (int i = 0; i < 4 && fields[i]; i++)
		f[i] = fields[i];
	access_entry->list |= NewsgroupFilter(f[0]);
	access_entry->read |= NewsgroupFilter(f[1]);
	access_entry->postTo |= NewsgroupFilter(f[2]);
	access_entry->modifyAccessFlags(f[3]);
}

int check_auth_file (ClientData *clt)
{
	AccessEntry *access_entry = clt->access_entry;
	string type("file:");

	if (access_entry->authentication.typeEqual (type))
		return -1;

	string authfile = access_entry->authentication.getField(1);
	char **authv = seekfile(authfile.c_str(), clt->auth_user);
	int authc = 0;
	int passf=-1;

	if (!authv)
		return -1;

	// user:hash is enough; missing list, read, postto and allow fields
	// add nothing, like empty ones.
	while (authv[authc])
		authc++;
	if (authc < 2)
		return -1;

	// a locked or empty field never matches
	if (authv[1][0] == '\0' || authv[1][0] == '*' || authv[1][0] == '!')
		return -1;
	passf = auto_cryptcheck(clt->auth_pass, authv[1]);
#ifdef PLAIN_TEXT_PASSWORDS
	// (only for a field that is no hash; the plain comparison came
	// first and accepted a hash as its own password)
	if (passf && plain_password_field(authv[1]))
		passf = hashes_differ(clt->auth_pass, authv[1]);
#endif
	if (passf)
		return -1;

	// Now we build a authentication string like the
	// unix:::: authentication string and set authentication
	// Herbert Straub
	access_entry->authentication.set (type+authfile);
	for (authc=2; authv[authc]; authc++) {
		access_entry->authentication.appendField(authv[authc]);
	}
	add_auth_line_rights(access_entry, authv + 2);
	slog.p(Logger::Debug) << "check_auth_file::NewsgroupFilter set to: "
		<< access_entry->list.getRulelist() << ":"
		<< access_entry->read.getRulelist() << ":"
		<< access_entry->postTo.getRulelist() << "\n";

	return 0;
}

#ifdef PAM_AUTH
int check_pam_user_pass (ClientData *clt)
{
	int retval;

	clt->conv.appdata_ptr = clt;
	clt->conv.conv = pam_conv_func;
	clt->pamh = NULL;
	retval =
	    pam_start(clt->access_entry->PAMServicename.c_str(), clt->auth_user,
		      &(clt->conv), &(clt->pamh));
	slog.p(Logger::Debug) << "check_pam_user_pass: pam_start returns "
		<< retval << "\n";
	// The client's address as PAM_RHOST, so that host-based rules
	// (pam_access, pam_rhosts, ...) apply; without it they saw no host
	// and didn't restrict anything. If it can't be set, the login fails
	// rather than going on without the host. (Empty in inetd mode
	// without a peer address: then there is no host to give.)
	if (retval == PAM_SUCCESS && clt->client_addr[0] != '\0') {
		retval = pam_set_item(clt->pamh, PAM_RHOST, clt->client_addr);
		slog.p(Logger::Debug) << "check_pam_user_pass: PAM_RHOST "
			<< clt->client_addr << ": " << retval << "\n";
	}
	if (retval == PAM_SUCCESS) {
		retval = pam_authenticate(clt->pamh, 0);	/* is user really user? */
		slog.p(Logger::Debug) << "check_pam_user_pass: pam_authenticate returns "
			<< retval << "\n";
	}
	if (retval == PAM_SUCCESS) {
		retval = pam_acct_mgmt(clt->pamh, 0);	/* permitted access? */
		slog.p(Logger::Debug) << "check_pam_user_pass: pam_acct_mgmt returns "
			<< retval << "\n";
	}
	if (retval != PAM_SUCCESS) {
		// pam_unix can't tell a wrong password from one it may not
		// check: as a user other than root, its helper unix_chkpwd only
		// checks that user's own password.
		slog.p(Logger::Notice) << "PAM service "
		    << clt->access_entry->PAMServicename << " refused "
		    << clt->auth_user << ": " << pam_strerror(clt->pamh, retval)
		    << (geteuid() != 0 ? " (NewsCache doesn't run as root, so"
			" pam_unix can't check local passwords of other users)" : "")
		    << "\n";
		pam_end(clt->pamh, retval);
		return -1;
	}
	pam_end(clt->pamh, retval);

	return 0;
}
int check_auth_pam_file (ClientData *clt)
{
/*
 * check_auth_pam_file code by Tuomo Pyhala <tuomo.pyhala@iki.fi>
 * Modify by Herbert Straub <h.straub@aon.at>:
 * 	using class Authentication
 * 	restructure source code
 */
	slog.p(Logger::Debug) << "in check_out_pam_file\n";
	AccessEntry *access_entry = clt->access_entry;
	string type("pam+file:");

	if (access_entry->authentication.typeEqual (type))
		return -1;

	if (check_pam_user_pass (clt))
		return -1;

	string authfile = access_entry->authentication.getField(1);
	char **authv;
	int found = 0;

	// The rights of every line that applies are added: the line "*"
	// (all users), the lines "@group" of the user's supplementary
	// groups, and the user's own line, in that order, so that a "!flag"
	// in the user's own line can still remove a right. Each line is
	// added on its own; the fields used to be joined as strings, without
	// a comma ("at.test" and "de.*" gave "at.testde.*", "post" twice
	// "postpost"), and lines of different lengths lost fields. A user
	// without a line of their own gets the rights of "*" and of their
	// groups; if no line applies at all, the login is refused.
	if ((authv = seekfile(authfile.c_str(), "*")) != NULL) {
		add_auth_line_rights(access_entry, authv + 1);
		found = 1;
	}
	struct group *gr;
	setgrent();
	while ((gr = getgrent()) != NULL) {
		for (int j = 0; gr->gr_mem[j]; j++) {
			if (!strcmp(gr->gr_mem[j], clt->auth_user)) {
				string group_entry = string("@") + gr->gr_name;
				authv = seekfile(authfile.c_str(),
						 group_entry.c_str());
				if (authv != NULL) {
					add_auth_line_rights(access_entry,
							     authv + 1);
					found = 1;
				}
				break;
			}
		}
	}
	endgrent();
	if ((authv = seekfile(authfile.c_str(), clt->auth_user)) != NULL) {
		add_auth_line_rights(access_entry, authv + 1);
		found = 1;
	}
	if (!found) {
		slog.p(Logger::Notice) << "pam+file: no line in " << authfile
		    << " applies to " << clt->auth_user << "\n";
		return -1;
	}
	slog.p(Logger::Debug) << "check_auth_pam_file::NewsgroupFilter set to: "
		<< access_entry->list.getRulelist() << ":"
		<< access_entry->read.getRulelist() << ":"
		<< access_entry->postTo.getRulelist() << "\n";

	return 0;
}

int check_auth_pam (ClientData *clt)
{
	AccessEntry *access_entry = clt->access_entry;
	string type("pam:");

	if (access_entry->authentication.typeEqual(type))
		return -1;

	if (check_pam_user_pass (clt))
		return -1;

	access_entry->list |= NewsgroupFilter(
			(access_entry->authentication.getField(1))
			.c_str());
	access_entry->read |= NewsgroupFilter(
			(access_entry->authentication.getField(2))
			.c_str());
	access_entry->postTo |= NewsgroupFilter(
			(access_entry->authentication.getField(3)).
			c_str());
	access_entry->modifyAccessFlags (access_entry->authentication.
			getField(4));
	slog.p(Logger::Debug) << "check_auth_pam::NewsgroupFilter set to: "
		<< access_entry->list.getRulelist() << ":"
		<< access_entry->read.getRulelist() << ":"
		<< access_entry->postTo.getRulelist() << "\n";

	return 0;
}
#endif /* PAM_AUTH */

/* the following formats may be specified (file/unix)
 * list:read:postto:allow
 */
int check_authentication(ClientData * clt)
{
	AccessEntry *access_entry = clt->access_entry;

	if (access_entry->authentication.getType() == "none") {
		return -1;
	}

	if (!strncmp((access_entry->authentication.getType()).c_str()
				, "unix", 4)) {
		return check_auth_unix (clt);
	} else
	    if (!strncmp((access_entry->authentication.getType()).c_str(),
				    "file", 4))
	{
		return check_auth_file (clt);
	}
#ifdef PAM_AUTH
	else if (!strncmp
		 ((access_entry->authentication.getType()).c_str(),
		  "pam+file", 8)) {
		return check_auth_pam_file (clt);
	}
	else if (!strncmp
		 ((access_entry->authentication.getType()).c_str(),
		  "pam", 3)) {
		return check_auth_pam (clt);
	}
#endif				/* #ifndef PAM_AUTH */
	// auth_{deny,none} is handled in nnrpd()
	return -1;
}


GroupInfo *selectgroup(ClientData * clt, const char *group)
{
	GroupInfo *gi;
	VERB(slog.
	     p(Logger::
	       Debug) << "NewsCache selectgroup (" << group << ")\n");

	// clt->groupname has room for MAXGROUPNAMELEN - 1 characters; longer
	// names can't exist (GroupInfo refuses them), but don't rely on that
	// before the strcpy below.
	if (strlen(group) >= sizeof(clt->groupname))
		return NULL;

	// NULL (411) only for a group that doesn't exist or that no news
	// server carries. Any other error (the news server can't be reached
	// or doesn't answer, the spool fails) goes to the command loop, which
	// answers 403 (RFC 3977 3.2.1): 411 told the client that the group
	// doesn't exist.
	try {
		gi = clt->srvr->groupinfo(group);
	} catch(const NoSuchGroupError & nsge) {
		return NULL;
	}
	// Reset Article Pointer
	if (gi->first() <= gi->last())
		clt->nbr = gi->first();
	else
		clt->nbr = 0;
	if (strcmp(clt->groupname, group) != 0) {
		if (clt->grp)
			clt->srvr->freegroup(clt->grp);
		clt->grp = NULL;
		clt->stat_groups++;
		if (clt->stat_artingrp) {
			slog.p(Logger::Notice) << clt->
			    client_logname << " group " << clt->
			    groupname << " " << clt->stat_artingrp << "\n";
			clt->stat_artingrp = 0;
		}
		strcpy(clt->groupname, group);
	} else {
		//FIXME! NServer has to maintain a list of all the allocated
		//FIXME! newsgroups and should call this function itself!!!
		if (clt->grp)
			clt->grp->setsize(gi->first(), gi->last());
	}
	return gi;
}

/*
 * Answers a command for a group or an article the client may not read:
 * 480 while a login could still give it the right (RFC 4643), 502 once it
 * has logged in or when its entry has no Authentication.
 */
static void access_denied(ClientData * clt)
{
	if (clt->auth_state != AUTH_OK)
		(*clt->co) << "480 authentication required\r\n";
	else
		(*clt->co) << "502 access denied\r\n";
}

/*
 * Whether the client may read an article fetched by message-ID: at least
 * one of its groups must match the client's Read list, as for GROUP.
 */
static int may_read_article(ClientData * clt, const Article & a)
{
	std::string ngs;
	try {
		ngs = a.getfield("Newsgroups:");
	}
	catch(const NoSuchFieldError & nsfe) {
		return 0;
	}
	// (split_newsgroups, as POST and the posting use it: a header that
	// reads differently to different parsers gives no right)
	std::vector<std::string> groups;
	if (!split_newsgroups(ngs, groups))
		return 0;
	for (std::vector<std::string>::size_type i = 0; i < groups.size(); i++)
		if (clt->access_entry->read.matches(groups[i].c_str()) > 0)
			return 1;
	return 0;
}

/*
 * Opens group for spool_locate and spool_article: the client's current
 * group if it is that one (*own = false), else a new instance (*own =
 * true, to be freed with freegroup).
 */
static Newsgroup *open_group(ClientData * clt, const string & group,
			     bool * own)
{
	*own = false;
	if (clt->grp && group == clt->groupname)
		return clt->grp;
	Newsgroup *g = clt->srvr->getgroup(group.c_str());
	*own = true;
	return g;
}

/*
 * The spool's record of the article with message-ID id, found with the
 * history: the group and number of a location whose record in the spool
 * has this Message-ID (the client's current group first), and that
 * overview record. Locations whose record is gone or holds another
 * article are dropped from the history. Returns 0 if the spool has none.
 */
// Orders history locations: the client's current group, then groups the
// client may read, then the others.
struct LocationOrder {
	ClientData *clt;
	LocationOrder(ClientData * c):clt(c) {
	}
	int rank(const History::Location & l) const {
		if (l.group == clt->groupname)
			return 0;
		return clt->access_entry->read.matches(l.group.c_str()) > 0 ? 1 : 2;
	}
	bool operator() (const History::Location & a,
			 const History::Location & b) const {
		return rank(a) < rank(b);
	}
};

static int spool_locate(ClientData * clt, const char *id, string & group,
			artnum_t & nbr, string & over)
{
	vector < History::Location > locs;
	if (History::lookup(id, locs) <= 0)
		return 0;
	// the current group first, then the groups the client may read
	std::stable_sort(locs.begin(), locs.end(), LocationOrder(clt));
	OverviewFmt *of = clt->srvr->overviewfmt();
	for (size_t i = 0; i < locs.size(); i++) {
		Newsgroup *g = NULL;
		bool own = false;
		string o;
		try {
			g = open_group(clt, locs[i].group, &own);
			// the spool's record only: no synchronization
			NVNewsgroup *nvg = dynamic_cast < NVNewsgroup * >(g);
			const char *p;
			if (nvg && (p = nvg->NVNewsgroup::getover(locs[i].nbr)))
				o = p;
		}
		catch(...) {
			o = "";
		}
		if (own && g)
			clt->srvr->freegroup(g);
		if (!o.empty() && of->getfield(o.c_str(), "Message-ID:", 0) == id) {
			group = locs[i].group;
			nbr = locs[i].nbr;
			over = o;
			return 1;
		}
		History::remove(id, locs[i].group.c_str());
	}
	return 0;
}

/*
 * The article with message-ID id from the spool. If the spool has only
 * its overview record, it is fetched by number from the news server and
 * stored, so an article crossposted to several groups is fetched once.
 * Returns 0 if the spool doesn't have it (or the fetch failed).
 */
static int spool_article(ClientData * clt, const char *id, Article & a)
{
	string group, over;
	artnum_t nbr;
	if (!spool_locate(clt, id, group, nbr, over))
		return 0;
	Newsgroup *g = NULL;
	bool own = false;
	Article *p = NULL;
	int ok = 0;
	try {
		g = open_group(clt, group, &own);
		NVNewsgroup *nvg = dynamic_cast < NVNewsgroup * >(g);
		// Don't fetch by number into a group the client may not read
		// (the article goes upstream by message-ID then; may_read_article
		// decides). An article the spool already has is served.
		if (nvg && clt->access_entry->read.matches(group.c_str()) <= 0 &&
		    !nvg->spooled(nbr))
			nvg = NULL;
		if (nvg)
			p = nvg->NVNewsgroup::getarticle(nbr);
		if (p) {
			ok = p->getfield("Message-ID:") == id;
			if (ok)	// (no operator=: _ctext points into _text)
				a.settext(p->GetText());
		}
	}
	catch(...) {
		ok = 0;
	}
	if (p)
		g->freearticle(p);
	if (own && g)
		clt->srvr->freegroup(g);
	return ok;
}

/*
 * Whether the groups in the Xref field of an overview record allow the
 * client to read the article (like may_read_article with Newsgroups):
 * 1 or 0, or -1 if the record has no Xref.
 */
static int may_read_xref(ClientData * clt, const string & over)
{
	string xref;
	try {
		xref = clt->srvr->overviewfmt()->getfield(over.c_str(), "Xref:", 0);
	}
	catch(...) {
		return -1;
	}
	// "host group:number group:number ..."
	std::string::size_type i = xref.find(' ');
	if (i == std::string::npos)
		return -1;
	int any = 0;
	while (i < xref.size()) {
		while (i < xref.size() && xref[i] == ' ')
			i++;
		std::string::size_type e = xref.find(' ', i);
		if (e == std::string::npos)
			e = xref.size();
		std::string tok = xref.substr(i, e - i);
		std::string::size_type c = tok.rfind(':');
		if (c != std::string::npos && c > 0) {
			any = 1;
			if (clt->access_entry->read.
			    matches(tok.substr(0, c).c_str()) > 0)
				return 1;
		}
		i = e;
	}
	return any ? 0 : -1;
}

/*
 * Fetches the article with message-ID id, from the spool if it is there
 * (see spool_article), else from the news server, and checks that the
 * client may read it. Returns 0, or -1 after an error reply (430, 403,
 * or 480/502).
 */
static int fetch_readable(ClientData * clt, const char *id, Article & a)
{
	try {
		if (!spool_article(clt, id, a))
			clt->srvr->article(id, &a);
	}
	catch(const NoSuchArticleError & nsae) {
		(*clt->co) << "430 no such article id found\r\n";
		return -1;
	}
	catch(const Error & e) {
		// (also a ResponseError: the news server's reply, which may
		// be a 220 or 381, is not the client's)
		(*clt->co) << "403 upstream failure\r\n";
		e.print();
		return -1;
	}
	if (!may_read_article(clt, a)) {
		access_denied(clt);
		return -1;
	}
	return 0;
}

void nsh_particle(ClientData * clt, char *cmnd, artnum_t nbr, Article * a)
{
	string aid = a->getfield("Message-ID:");

	switch (cmnd[0]) {
	case 'a':
		(*clt->co) << "220 " << nbr << " " << aid
		    << " article retrieved - head and body follow\r\n";
		(*clt->co) << *a << ".\r\n";
		slog.p(Logger::Info) << "articlesize " << clt->
		    groupname << ":" << nbr << " " << a->length() << "\n";
		break;
	case 'h':
		(*clt->co) << "221 " << nbr << " " << aid
		    << " article retrieved - head follows\r\n";
		a->write(*clt->co, Article::Head);
		(*clt->co) << ".\r\n";
		break;
	case 'b':
		(*clt->co) << "222 " << nbr << " " << aid
		    << " article retrieved - body follows\r\n";
		a->write(*clt->co, Article::Body);
		(*clt->co) << ".\r\n";
		slog.p(Logger::Info) << "articlesize " << clt->
		    groupname << ":" << nbr << " " << a->length() << "\n";
		break;
	}
}


// RFC 4643 2.3: 481 for a failed login, 502 only where AUTHINFO isn't
// available at all (after a login, or no Authentication for the entry).
#define REJECT_SEQUENCE 		"502"
#define FAILED_SEQUENCE 		"481"
#define PASS_NEEDED_SEQUENCE 		"381"
#define OK_SEQUENCE 			"281"
#define INCORRECT_ORDER_SEQUENCE 	"482"

// Overwrites a password in memory (a memset the compiler can't drop),
// so that it isn't kept for the whole session, e.g. for a core dump.
static void clear_password(char *p)
{
	volatile char *v = p;
	while (*v)
		*v++ = '\0';
}

//Authinfo
int ns_authinfo(ClientData * clt, int argc, char *argv[])
{
	clt->auth_refused = false;
	// No AUTHINFO after a login (RFC 4643 2.2: 502); a second login
	// added that user's rights to the first one's. Entries without an
	// Authentication start out in AUTH_OK: no login for them either.
	if (clt->auth_state == AUTH_OK) {
		if (clt->access_entry->authentication.getType() == "none")
			(*clt->co) << REJECT_SEQUENCE
			    " no authentication available\r\n";
		else
			(*clt->co) << REJECT_SEQUENCE
			    " already authenticated\r\n";
		return -1;
	}
	// AuthRequiresTLS: no user name or password over a plain connection
	// (RFC 4643 2.2, 2.3.2: 483). Before the name is stored, so that a
	// PASS after it gets 483 too, not 482.
	if (clt->access_entry->auth_requires_tls && !clt->tls) {
		clt->auth_user_valid = false;
		if (argc >= 3 && !strcasecmp(argv[1], "pass"))
			clear_password(argv[2]);
		(*clt->co) << "483 Encryption required, connect with TLS\r\n";
		return -1;
	}
	// check arguments
	if (argc != 3)
		goto ns_authinfo_error;

	if (!strcasecmp(argv[1], "user")) {
		if (strlen(argv[2]) > sizeof(clt->auth_user) - 1) {
			// a failed USER also ends the pending one: the next
			// PASS gets 482, not a check against the earlier name
			clt->auth_user_valid = false;
			(*clt->co) << FAILED_SEQUENCE " argument too long\r\n";
			return -1;
		}
		strcpy(clt->auth_user, argv[2]);
		clt->auth_user_valid = true;
		(*clt->co) << PASS_NEEDED_SEQUENCE " password please\r\n";
		return 0;
	}

	if (!strcasecmp(argv[1], "pass")) {
		if (!clt->auth_user_valid || clt->auth_user[0] == '\0') {
			(*clt->
			 co) << INCORRECT_ORDER_SEQUENCE
		      " issue authinfo user first\r\n";
			return -1;
		}
		// (after the sequence check: PASS without USER is 482 in
		// any case, RFC 4643 2.3.2)
		if (strlen(argv[2]) > sizeof(clt->auth_pass) - 1) {
			clear_password(argv[2]);
			clt->auth_user_valid = false;
			(*clt->co) << FAILED_SEQUENCE " argument too long\r\n";
			return -1;
		}
		strcpy(clt->auth_pass, argv[2]);
		clear_password(argv[2]);
		int checked = check_authentication(clt);
		clear_password(clt->auth_pass);
		// Keep the name for the failed-login audit log, but require another
		// USER before a subsequent PASS (RFC 4643 2.3.2).
		clt->auth_user_valid = false;

		if (checked < 0) {
			clt->auth_refused = true;
			// the same short delay after each failure; it grew with
			// the failures (15, 30, 45 s by default), which only held
			// the session's process and memory
			++clt->auth_failures;
			sleep(clt->auth_failure_sleep_time);
			(*clt->
			 co) << FAILED_SEQUENCE << " Bad authinfo.\r\n";
			return -1;
		}

		(*clt->co) << OK_SEQUENCE << " OK.\r\n";
		// (a failed login is logged with the user name too)
		slog.p(Logger::Notice) << clt->client_logname
		    << " authenticated as " << clt->auth_user << "\n";
		clt->auth_state = AUTH_OK;
		set_client_command_table(*clt);

		return 0;
	}
	// error
      ns_authinfo_error:
	if (argc > 1) {
		switch (tolower(argv[1][0])) {
		case 'u':
			(*clt->
			 co) << "501 Syntax: authinfo user username\r\n";
			return -1;
		case 'p':
			(*clt->
			 co) << "501 Syntax: authinfo pass password\r\n";
			return -1;
		}
	}
	(*clt->
	 co) << "501 Syntax: authinfo [user username|pass password]\r\n";
	return -1;
}

int ns_article(ClientData * clt, int argc, char *argv[])
{
	if (argc > 2)
		goto ns_article_error;

	// retrieve article|body|head by number?
	if (argc == 1 || isdigit(argv[1][0])) {
		if (clt->groupname[0] == '\0') {
			(*clt->
			 co) << "412 no newsgroup has been selected\r\n";
			return -1;
		}
		try {
			if (!clt->grp)
				clt->grp =
				    clt->srvr->getgroup(clt->groupname);
		}
		catch(const NoSuchGroupError & nsge) {
			(*clt->co) << "412 current group no longer exists\r\n";
			return -1;
		}
		catch(const Error & e) {
			(*clt->co) << "403 operation failed\r\n";
			return -1;
		}

		if (argc == 1 && clt->nbr == 0) {
			(*clt->
			 co) <<
		      "420 no current article has been selected\r\n";
			return -1;
		}

		artnum_t nbr = clt->nbr;
		const char *end;
		if (argc == 2 && (!parse_artnum(argv[1], &nbr, &end) || *end))
			goto ns_article_error;

		ASSERT2(clt->grp->testdb());
		Article *a;

		if ((a = clt->grp->getarticle(nbr)) == NULL) {
			(*clt->
			 co) <<
		      "423 no such article number in this group\r\n";
			return -1;
		}
		nsh_particle(clt, argv[0], nbr, a);
		clt->grp->freearticle(a);
		clt->nbr = nbr;
		clt->stat_artingrp++;
		clt->stat_articles++;
		return 0;
	}
	// retrieve article|body|head by id?
	if (argv[1][0] == '<') {
		// from the spool if it has the article (the history), else
		// from the news server
		Article a;
		if (fetch_readable(clt, argv[1], a) < 0)
			return -1;
		nsh_particle(clt, argv[0], 0, &a);
		clt->stat_artingrp++;
		clt->stat_articles++;
		return 0;
	}
	// error
      ns_article_error:
	switch (argv[0][0]) {
	case 'a':
		(*clt->co) << "501 Syntax: article [nbr|<id>]\r\n";
		return -1;
	case 'h':
		(*clt->co) << "501 Syntax: head [nbr|<id>]\r\n";
		return -1;
	case 'b':
		(*clt->co) << "501 Syntax: body [nbr|<id>]\r\n";
		return -1;
	}
	return -1;
}

int ns_stat(ClientData * clt, int argc, char *argv[])
{
	if (argc > 2)
		goto ns_stat_error;

	// stat by number?
	if (argc == 1 || isdigit(argv[1][0])) {
		// fetch article by number
		if (clt->groupname[0] == '\0') {
			(*clt->
			 co) << "412 no newsgroup has been selected\r\n";
			return -1;
		}
		try {
			if (!clt->grp)
				clt->grp =
				    clt->srvr->getgroup(clt->groupname);
		}
		catch(const NoSuchGroupError & nsge) {
			(*clt->co) << "412 current group no longer exists\r\n";
			return -1;
		}
		catch(const Error & e) {
			(*clt->co) << "403 operation failed\r\n";
			return -1;
		}

		if (argc == 1 && clt->nbr == 0) {
			(*clt->
			 co) <<
		      "420 no current article has been selected\r\n";
			return -1;
		}

		artnum_t nbr = clt->nbr;
		const char *end;
		if (argc == 2 && (!parse_artnum(argv[1], &nbr, &end) || *end))
			goto ns_stat_error;

		ASSERT2(clt->grp->testdb());
		// The overview record says whether the article exists; no
		// need to fetch it. getover gives NULL or "" for none.
		OverviewFmt *of = clt->srvr->overviewfmt();
		const char *o = clt->grp->getover(nbr);
		string mid;
		if (o && *o)
			mid = of->getfield(o, "Message-ID:", 0);
		if (mid.empty()) {
			(*clt->
			 co) <<
		      "423 no such article number in this group\r\n";
			return -1;
		}
		(*clt->co) << "223 " << nbr << " " << mid << " status\r\n";
		clt->nbr = nbr;
		return 0;
	}
	// stat by id?
	if (argv[1][0] == '<') {
		// The spool's overview record is enough when its Xref names a
		// group the client may read (Xref's groups are among the
		// article's Newsgroups). Otherwise decide as ARTICLE does, from
		// the Newsgroups header: Xref may list fewer groups.
		string group, over;
		artnum_t nbr;
		if (spool_locate(clt, argv[1], group, nbr, over) &&
		    may_read_xref(clt, over) > 0) {
			(*clt->co) << "223 0 " << argv[1] << " status\r\n";
			return 0;
		}
		Article a;
		if (fetch_readable(clt, argv[1], a) < 0)
			return -1;
		string mid = a.getfield("Message-ID:");
		(*clt->co) << "223 0 " << mid << " status\r\n";
		return 0;
	}
	// error
      ns_stat_error:
	(*clt->co) << "501 Syntax: stat [nbr|<id>]\r\n";
	return -1;
}

/**
 * \author Thomas Gschwind
 * Returns the gmtime. See RFC2980 DATE command.
 * \returns 0 on success, -1 on error
 */
int ns_date(ClientData * clt, int argc, char *argv[])
{
	struct tm *ltm, tmbuf;
	time_t conv;
	char buf[256];

	if (argc != 1) {
		(*clt->co) << "501 Syntax: date\r\n";
		return -1;
	}
	time(&conv);
	if ((ltm = gmtime_r(&conv, &tmbuf)) == NULL) {
		// 403: internal fault (RFC 3977 3.2.1); 500 is "unknown
		// command"
		(*clt->co) << "403 gmtime failed\r\n";
		slog.p(Logger::Error) << "ns_date() gmtime failed\n";
		return -1;
	}
	sprintf(buf, "111 %04d%02d%02d%02d%02d%02d\r\n",
		1900 + ltm->tm_year, ltm->tm_mon + 1, ltm->tm_mday,
		ltm->tm_hour, ltm->tm_min, ltm->tm_sec);
	(*clt->co) << buf;

	return 0;
}

/**
 * \author Thomas Gschwind
 * Find the news server responsible for this newsgroup.
 * Connect to it and select the newsgroup.
 */
int ns_group(ClientData * clt, int argc, char *argv[])
{
	if (argc != 2) {
		(*clt->co) << "501 Syntax: group Newsgroup\r\n";
		return -1;
	}

	if (clt->access_entry->read.matches(argv[1]) <= 0) {
		access_denied(clt);
		return 0;
	}

	GroupInfo *gi;
	if ((gi = selectgroup(clt, argv[1])) == NULL) {
		(*clt->co) << "411 no such news group\r\n";
		return -1;
	}

	(*clt->co) << "211 "
	    << gi->n() << " "
	    << gi->first() << " "
	    << gi->last() << " " << gi->name() << " group selected\r\n";

	slog.p(Logger::Info) << "groupsize [" << gi->first() << ","
	    << gi->last() << "]\n";

	return 0;
}

/**
 * \author Thomas Gschwind
 * Lists the available commands.
 */
int ns_help(ClientData * clt, int argc, char *argv[])
{
	map < string, nnrp_command_t * >::iterator begin =
	    clt->client_command_map.begin(), end = clt->client_command_map.end();

	VERB(slog.p(Logger::Debug) << "NewsCache::ns_help:\n");
	(*clt->co) << "100 Legal commands\r\n";
	while (begin != end) {
		if (begin->second->func) {
			(*clt->co) << "  " << begin->
			    first << " " << begin->second->desc << "\r\n";
		}
		++begin;
	}
	(*clt->co) << "Report problems to " << Cfg.Admin << "\r\n.\r\n";
	return 0;
}

/* last,next */
int ns_lastnext(ClientData * clt, int argc, char *argv[])
{
	if (argc != 1) {
		switch (argv[0][0]) {
		case 'l':
			(*clt->co) << "501 Syntax: last\r\n";
			return -1;
		case 'n':
			(*clt->co) << "501 Syntax: next\r\n";
			return -1;
		}
	}
	if (clt->groupname[0] == '\0') {
		(*clt->co) << "412 No newsgroup currently selected\r\n";
		return -1;
	}
	if (clt->nbr == 0) {
		(*clt->
		 co) << "420 no current article has been selected\r\n";
		return -1;
	}
	try {
		if (!clt->grp)
			clt->grp = clt->srvr->getgroup(clt->groupname);
	}
	catch(const NoSuchGroupError & nsge) {
		(*clt->co) << "412 current group no longer exists\r\n";
		return -1;
	}
	catch(const Error & e) {
		(*clt->co) << "403 operation failed\r\n";
		return -1;
	}

	artnum_t f, i = clt->nbr, l;
	const char *o;
	string aid;
	OverviewFmt *ofmt = clt->srvr->overviewfmt();
	clt->grp->getsize(&f, &l);
	switch (argv[0][0]) {
	case 'l':
		for (;;) {
			// check before counting down: i - 1 wraps around at 0
			if (i <= f) {
				(*clt->
				 co) << "422 No previous to retrieve\r\n";
				return -1;
			}
			i--;
			o = clt->grp->getover(i);
			// (NULL from a not-cached group: no record)
			if (o && o[0]) {
				aid = ofmt->getfield(o, "Message-ID:", 0);
				break;
			}
		}
		break;
	case 'n':
		for (;;) {
			if (i >= l) {
				(*clt->
				 co) << "421 No next to retrieve\r\n";
				return -1;
			}
			++i;
			o = clt->grp->getover(i);
			// (NULL from a not-cached group: no record)
			if (o && o[0]) {
				aid = ofmt->getfield(o, "Message-ID:", 0);
				break;
			}
		}
	}
	clt->nbr = i;

	(*clt->co) << "223 " << clt->nbr << " " << aid
	    << " article retrieved - request text separately\r\n";
	return 0;
}

class print_list_active {
	ostream & os;
      public:
	print_list_active(ostream & os):os(os) {
	} void operator() (const GroupInfo & gd) {
		os << gd << "\r\n";
		if (!os.good())
			throw SystemError("cannot write to clientstream",
					  errno, ERROR_LOCATION);
	}
};

class print_list_active_times {
	ostream & os;
      public:
	print_list_active_times(ostream & os):os(os) {
	} void operator() (const GroupInfo & gd) {
		os << gd.name() << " " << gd.ctime() << " news\r\n";
		if (!os.good())
			throw SystemError("cannot write to clientstream",
					  errno, ERROR_LOCATION);
	}
};

template < class Format > class active_filter_group {
	Format fmt;
	const NewsgroupFilter & filter;
      public:
	active_filter_group(ostream & os, const NewsgroupFilter & filter)
	:fmt(os), filter(filter) {
	}
	void operator() (const GroupInfo & gd) {
		if (filter.matches(gd.name()) > 0)
			fmt(gd);
	}
};

template < class Format > class active_filter_group_time {
	Format fmt;
	const NewsgroupFilter & filter;
	nvtime_t ctime;
      public:
	active_filter_group_time(ostream & os,
				 const NewsgroupFilter & filter,
				 nvtime_t ctime)
	:fmt(os), filter(filter), ctime(ctime) {
	}
	void operator() (const GroupInfo & gd) {
		if (gd.ctime() > ctime && filter.matches(gd.name()) > 0)
			fmt(gd);
	}
};

template < class Format > class active_filter_time {
	Format fmt;
	nvtime_t ctime;
      public:
	active_filter_time(ostream & os, nvtime_t ctime)
      :    fmt(os), ctime(ctime) {
	}
	void operator() (const GroupInfo & gd) {
		if (gd.ctime() > ctime)
			fmt(gd);
	}
};

/*
 * Runs fn over every group of the active database under a shared lock.
 * The walk follows offsets through this process's mapping of the file:
 * without the lock, a session that adds groups meanwhile grows the file,
 * and the walk took the sound file for damaged (moved to .bad, every
 * group new for NEWGROUPS) and ended the list early. fn writes to a
 * buffer; the caller sends it after the lock is released, so that a slow
 * client doesn't hold up the sessions that update the list.
 */
template < class Fn > static void collect_active(ActiveDB * active, Fn fn)
{
	ActiveDBLock lck(active);
	for_each(active->begin(), active->end(), fn);
}

/* List [active|newsgroups|overview.fmt]
 * List the specified data
 */
/*
 * The overview field that HDR reads for name when it is given a range
 * or the current article, without the colon ("Subject", "Bytes" for
 * ":bytes"), or "" if there is none. The overview's Bytes and Lines are
 * metadata, so the headers of these names are not offered (RFC 3977
 * 8.5: HDR returns the article's header).
 */
static string hdr_overview_field(OverviewFmt * of, const char *name)
{
	string want;

	if (strcasecmp(name, ":bytes") == 0)
		want = "Bytes:";
	else if (strcasecmp(name, ":lines") == 0)
		want = "Lines:";
	else if (name[0] == ':' || strcasecmp(name, "bytes") == 0 ||
		 strcasecmp(name, "lines") == 0)
		return "";
	else
		want = string(name) + ':';
	for (int i = 0; i < of->nfields(); i++) {
		if (strcasecmp(of->fieldname(i), want.c_str()) == 0) {
			string f = of->fieldname(i);
			return f.substr(0, f.size() - 1);
		}
	}
	return "";
}

/*
 * LIST HEADERS [MSGID|RANGE] (RFC 3977 8.6): by message-ID HDR fetches
 * the article, so it has every header (":") and :bytes; by number it
 * has the overview fields. Without an argument, what both forms have.
 */
static void list_headers(ClientData * clt, const char *form)
{
	OverviewFmt *of = clt->srvr->overviewfmt();
	bool msgid = form && strcasecmp(form, "msgid") == 0;
	bool range = form && strcasecmp(form, "range") == 0;

	(*clt->co) << "215 headers and metadata items supported:\r\n";
	if (msgid) {
		(*clt->co) << ":\r\n";
	} else {
		for (int i = 0; i < of->nfields(); i++) {
			string f = of->fieldname(i);
			f = f.substr(0, f.size() - 1);
			if (strcasecmp(f.c_str(), "bytes") == 0 ||
			    strcasecmp(f.c_str(), "lines") == 0)
				continue;
			(*clt->co) << f << "\r\n";
		}
		if (range)
			(*clt->co) << ":lines\r\n";
	}
	(*clt->co) << ":bytes\r\n";
	(*clt->co) << ".\r\n";
}

int ns_list(ClientData * clt, int argc, char *argv[])
{
	if (argc <= 3) {
		if (argc == 1 || strcasecmp(argv[1], "active") == 0) {
			ActiveDB *active;
			active = clt->srvr->active();
			NewsgroupFilter filter(clt->access_entry->list);
			if (argc == 3)
				filter.setWildmat(argv[2]);
			// collected under the lock, sent after it (see
			// collect_active)
			ostringstream lines;
			collect_active(active, active_filter_group <
				       print_list_active > (lines, filter));
			(*clt->
			 co) <<
		      "215 List of newsgroups (Group High Low Flags) follows\r\n"
			    << lines.str() << ".\r\n";
			return 0;
		} else if (strcasecmp(argv[1], "active.times") == 0) {
			ActiveDB *active;
			active = clt->srvr->active();
			NewsgroupFilter filter(clt->access_entry->list);
			if (argc == 3)
				filter &= NewsgroupFilter(argv[2]);
			ostringstream lines;
			collect_active(active, active_filter_group <
				       print_list_active_times > (lines,
								  filter));
			(*clt->co) << "215 information follows\r\n"
			    << lines.str() << ".\r\n";
			return 0;
		} else if (strcasecmp(argv[1], "newsgroups") == 0) {
			// LIST NEWSGROUPS [wildmat] (RFC 3977 7.6.6): only the
			// groups the client may list, like LIST ACTIVE. The
			// wildmat form answered 501, and every description in
			// the file was shown.
			char buf[MAXPATHLEN];
			string strg;
			if (snprintf(buf, sizeof(buf), "%s/.newsgroups",
				     Cfg.SpoolDirectory) >= (int) sizeof(buf))
				buf[0] = '\0';	// can't happen: see LServer::init
			ifstream ifs(buf);
			NewsgroupFilter filter(clt->access_entry->list);
			(*clt->
			 co) << "215 Description follows\r\n";
			for (;;) {
				nlreadline(ifs, strg);
				if (!ifs.good())
					break;
				string name =
				    strg.substr(0, strg.find_first_of(" \t\r\n"));
				if (filter.matches(name.c_str()) <= 0 ||
				    (argc == 3 && !wildmat(name.c_str(), argv[2])))
					continue;
				(*clt->co) << strg;
			}
			(*clt->co) << ".\r\n";
			return 0;
		} else if (argc == 2) {
			if (strcasecmp(argv[1], "extensions") == 0) {
				(*clt->
				 co) << "202 extensions supported\r\n";
				(*clt->co) << " over\r\n";
				(*clt->co) << " pat\r\n";
				(*clt->co) << " authinfo user\r\n";
				(*clt->co) << ".\r\n";
				return 0;
			} else if (strcasecmp(argv[1], "headers") == 0) {
				list_headers(clt, NULL);
				return 0;
			} else if (strcasecmp(argv[1], "overview.fmt") ==
				   0) {
				(*clt->
				 co) <<
			      "215 Order of fields in overview database\r\n"
			      << *(clt->srvr->overviewfmt()) << ".\r\n";
				return 0;
			}
		}
	}

	if (argc == 3 && strcasecmp(argv[1], "headers") == 0 &&
	    (strcasecmp(argv[2], "msgid") == 0 ||
	     strcasecmp(argv[2], "range") == 0)) {
		list_headers(clt, argv[2]);
		return 0;
	}

	(*clt->
	 co) <<
      "501 Syntax: list [active [wildmat]|active.times [time]|extensions|headers [msgid|range]|newsgroups [wildmat]|overview.fmt]\r\n";
	return -1;
}

/* listgroup [group [range]] (RFC 3977 6.1.2)
 * The numbers come from the news server's LISTGROUP as they arrive, or,
 * if it has none, from the overview records.
 */
int ns_listgroup(ClientData * clt, int argc, char *argv[])
{
	artnum_t fst = 0, lst = ARTNUM_MAX;
	const char *p;
	GroupInfo *gi;

	if (argc > 3) {
	      ns_listgroup_syntax:
		(*clt->co) << "501 Syntax: listgroup [newsgroup [range]]\r\n";
		return -1;
	}
	if (argc == 3) {
		// n, n- or n-m
		if (!parse_artnum(argv[2], &fst, &p))
			goto ns_listgroup_syntax;
		lst = fst;
		if (*p == '-') {
			lst = ARTNUM_MAX;
			p++;
			if (isdigit((unsigned char) *p) &&
			    !parse_artnum(p, &lst, &p))
				goto ns_listgroup_syntax;
		}
		if (*p != '\0')
			goto ns_listgroup_syntax;
	}
	if (argc == 1 && clt->groupname[0] == '\0') {
		(*clt->co) << "412 Not currently in a newsgroup\r\n";
		return -1;
	}
	if (argc >= 2) {
		// Check before selecting: a refused LISTGROUP must not make
		// the group the current one (XOVER, ARTICLE n, ... read the
		// current group without checking again).
		if (clt->access_entry->read.matches(argv[1]) <= 0) {
			access_denied(clt);
			return 0;
		}
		if ((gi = selectgroup(clt, argv[1])) == NULL) {
			(*clt->co) << "411 no such news group\r\n";
			return -1;
		}
	} else {
		// selects the group anew, the current article is its first
		if ((gi = selectgroup(clt, clt->groupname)) == NULL) {
			(*clt->co) << "412 current group no longer exists\r\n";
			return -1;
		}
	}

	// (gi points to static data, which getgroup may change)
	GroupInfo ginfo = *gi;
	try {
		if (!clt->grp)
			clt->grp = clt->srvr->getgroup(clt->groupname);
	}
	catch(const NoSuchGroupError & nsge) {
		if (argc >= 2)
			(*clt->co) << "411 no such news group\r\n";
		else
			(*clt->co) << "412 current group no longer exists\r\n";
		return -1;
	}
	catch(const Error & e) {
		(*clt->co) << "403 operation failed\r\n";
		e.print();
		return -1;
	}

	(*clt->co) << "211 " << ginfo.n() << " " << ginfo.first() << " "
	    << ginfo.last() << " " << clt->groupname << " list follows\r\n";
	clt->grp->printlistgroup(*clt->co, fst, lst);
	(*clt->co) << ".\r\n";
	return 0;
}

/*
 * The rights the client can use right now: none before a required login
 * (nnrpd() answers 480 to everything but AUTHINFO, CAPABILITIES, MODE
 * READER, HELP and QUIT then).
 */
static unsigned int current_rights(ClientData * clt)
{
	if (clt->auth_state == AUTH_REQUIRED)
		return 0;
	return clt->access_entry->access_flags;
}

/*
 * The greeting and the reply to MODE READER: 200 if the client may post
 * now, 201 otherwise (RFC 3977 5.1.1, 5.3).
 */
static void send_ready(ClientData * clt)
{
	if (current_rights(clt) & AccessEntry::af_post)
		(*clt->co) << "200 " PACKAGE " " VERSION
		    ", accepting NNRP commands\r\n";
	else
		(*clt->co) << "201 " PACKAGE " " VERSION
		    ", accepting NNRP commands, posting prohibited\r\n";
}

int ns_mode(ClientData * clt, int argc, char *argv[])
{
	if (argc != 2 ||
	    (strcasecmp(argv[1], "reader") != 0 &&
	     strcasecmp(argv[1], "query") != 0)) {
		(*clt->co) << "501 Syntax: mode (reader|query)\r\n";
		return -1;
	}
	// Answered before the login as well: a client sends MODE READER
	// before AUTHINFO, never after it (RFC 4643 2.2).
	send_ready(clt);
	return 0;
}

/*
 * NEWGROUPS date time [GMT]
 */
#define DD2INT(x) ((x)[0]-'0')*10+((x)[1]-'0')
int ns_newgroups(ClientData * clt, int argc, char *argv[])
{
	if (argc < 3 || argc > 4) {
	      ns_newgroups_error:
		(*clt->co) << "501 Syntax: NEWGROUPS date time [GMT]\r\n";
		return -1;
	}

	int i, y4, is_gmt = 0;
	struct tm ltm;
	time_t lt;

	memset(&ltm, 0, sizeof(ltm));
	char *p;

	// check first two arguments, y4 indicates whether year has 4 digits
	for (i = 0; isdigit(argv[2][i]); i++);
	if (i != 6 || argv[2][i])
		goto ns_newgroups_error;
	for (y4 = 0; isdigit(argv[1][y4]); y4++);
	if ((y4 != 8 && y4 != 6) || argv[1][y4])
		goto ns_newgroups_error;
	y4 -= 4;

	// check whether time/date is specified in GMT
	if (argc >= 4) {
		if (strcasecmp(argv[3], "gmt") != 0 &&
		    strcasecmp(argv[3], "utc") != 0) {
			goto ns_newgroups_error;
		}
		is_gmt = 1;
	}
	// fill in ltm with supplied values
	p = argv[1];
	if (y4 > 2) {
		i = DD2INT(p);
		p += 2;
		ltm.tm_year = i * 100 + DD2INT(p) - 1900;
		p += 2;
	} else {
		struct tm *now, nowbuf;
		int yr;

		// draft-ietf-nntpext-base-04.txt:
		// If the first two digits of the year are not specified,
		// the year is to be taken from the current century if YY
		// is smaller than or equal to the current year,
		// otherwise the year is from the previous century.
		time(&lt);
		now = is_gmt ? gmtime_r(&lt, &nowbuf) : localtime_r(&lt, &nowbuf);
		if (now == NULL)
			goto ns_newgroups_error;
		yr = now->tm_year % 100;

		ltm.tm_year = DD2INT(p);
		p += 2;
		if (ltm.tm_year > yr)
			ltm.tm_year += now->tm_year - yr - 100;
		else
			ltm.tm_year += now->tm_year - yr;
	}
	ltm.tm_mon = DD2INT(p) - 1;
	p += 2;			// tm_mon has a range of 0(Jan)--11(Dec)!!!
	ltm.tm_mday = DD2INT(p);

	p = argv[2];
	ltm.tm_hour = DD2INT(p);
	p += 2;
	ltm.tm_min = DD2INT(p);
	p += 2;
	ltm.tm_sec = DD2INT(p);

	// The instant of the given date and time: in UTC with timegm, or in
	// local time with mktime, which decides DST for that date. The DST
	// flag used to be forced (from "daylight", or from the current time)
	// and, on glibc (no HAVE_TZNAME), "GMT" wasn't applied at all: the
	// time was off by the UTC offset, or by an hour across DST.
	if (is_gmt) {
		lt = timegm(&ltm);
	} else {
		ltm.tm_isdst = -1;
		lt = mktime(&ltm);
	}
	if (lt == -1)
		goto ns_newgroups_error;

	// An error here goes to the command loop, as for LIST: 403 (RFC 3977
	// 3.2.1), or once more after a damaged database. (It answered 410,
	// which isn't a NEWGROUPS reply.)
	ActiveDB *active = clt->srvr->active();
	// in the format of LIST ACTIVE (RFC 3977 7.3.2), not LIST
	// ACTIVE.TIMES
	ostringstream lines;
	collect_active(active, active_filter_group_time <
		       print_list_active > (lines,
					    clt->access_entry->list, lt));
	(*clt->co) << "231 list of new groups follows\r\n"
	    << lines.str() << ".\r\n";
	return 0;
}

#undef DD2INT

/*
 * Reads a posted article up to its "." line into text, as readtext does
 * (lines end with "\r\n", the "." line not included). Returns 0; 1 when
 * the article is larger than max bytes (the rest is read and dropped up
 * to the "." line, so that the session stays in step, and nothing is
 * kept); -1 when the input ends before the "." line. Article::read had no
 * limit: an endless article, or an endless line, took all the memory.
 */
static int read_post_article(istream & is, string & text, size_t max)
{
	string line;
	size_t total = 0, len;
	bool over = false;
	char first, second;
	int c;

	text.clear();
	for (;;) {
		len = 0;
		first = second = 0;
		line.clear();
		while ((c = is.get()) != EOF && c != '\n') {
			if (len == 0)
				first = c;
			else if (len == 1)
				second = c;
			len++;
			if (!over) {
				line += (char) c;
				if (total + line.size() > max) {
					over = true;
					string().swap(line);
					string().swap(text);
				}
			}
		}
		if (c == EOF)
			return -1;
		if ((len == 1 && first == '.')
		    || (len == 2 && first == '.' && second == '\r'))
			break;
		if (over)
			continue;
		if (!line.empty() && line[line.size() - 1] == '\r')
			line.erase(line.size() - 1);
		total += line.size() + 2;
		if (total > max) {
			over = true;
			string().swap(text);
			continue;
		}
		text += line;
		text += "\r\n";
	}
	return over ? 1 : 0;
}

// Inspect every header name so that a valid first Message-ID cannot hide a
// malformed second one. A POST may omit the field; then post() generates it.
static bool valid_post_msgid(const string &text, const Article &art)
{
	size_t start = 0, count = 0;
	while (start < text.size()) {
		size_t end = text.find("\r\n", start);
		if (end == string::npos || end == start)
			break;
		if (end - start >= 11 &&
		    strncasecmp(text.c_str() + start, "Message-ID:", 11) == 0)
			count++;
		start = end + 2;
	}
	if (count == 0)
		return true;
	if (count != 1)
		return false;
	string id;
	try {
		id = art.getfield("message-id:");
	}
	catch(const Error &) {
		return false;
	}
	// RFC 5536 permits whitespace around the field value, not within it.
	while (!id.empty() && (id[id.size() - 1] == ' ' || id[id.size() - 1] == '\t'))
		id.erase(id.size() - 1);
	return strlen(id.c_str()) == id.size() && valid_msgid_5536(id.c_str());
}

int ns_post(ClientData * clt, int argc, char *argv[])
{
	VERB(slog.p(Logger::Debug) << "NewsCache::ns_post\n");
	if (argc != 1) {
		(*clt->co) << "501 Syntax: post\r\n";
		return -1;
	}

	Article art;

	try {
		// request article to be posted
		(*clt->co) << "340 send article to be posted.\r\n";
		clt->co->flush();
		string text;
		int r = read_post_article(*clt->ci, text,
					  (size_t) Cfg.MaxPostSize);
		if (r < 0) {
			// the input ended (or failed) before the "." line:
			// the article is cut off and must not be posted
			slog.p(Logger::Notice) << clt->client_logname
			    << " POST: the article ended without its final"
			    " \".\", not posted\n";
			(*clt->co) << "441 article incomplete, not posted\r\n";
			return -1;
		}
		if (r > 0) {
			slog.p(Logger::Notice) << clt->client_logname
			    << " POST: article larger than " << Cfg.MaxPostSize
			    << " bytes, not posted\n";
			(*clt->co) << "441 article too large (at most "
			    << Cfg.MaxPostSize << " bytes)\r\n";
			return -1;
		}
		art.settext(text);
		if (!valid_post_msgid(text, art)) {
			(*clt->co) << "441 invalid Message-ID header\r\n";
			return -1;
		}

		// check whether client is authorized to post this article.
		// The same split as RServer::post, which picks the servers
		// to post to: a name with white space in it ("a b", which
		// the matcher read as "a" and the posting as two groups)
		// is refused rather than read two ways.
		std::vector<std::string> groups;
		if (!split_newsgroups(art.getfield("newsgroups:"), groups)) {
			(*clt->co) << "441 invalid Newsgroups header\r\n";
			return -1;
		}
		if (groups.empty()) {
			(*clt->co) << "441 no newsgroups\r\n";
			return -1;
		}
		// After the article, RFC 3977 6.3.1 allows only 240 and 441:
		// a login that could add the group gets a hint in the text
		// (it was a 480).
		for (std::vector<std::string>::size_type i = 0;
		     i < groups.size(); i++) {
			if (clt->access_entry->postTo.
			    matches(groups[i].c_str()) <= 0) {
				(*clt->co) << "441 posting to " << groups[i]
				    << " not allowed"
				    << (clt->auth_state != AUTH_OK ?
					", authentication required" : "")
				    << "\r\n";
				return 0;
			}
		}
	}
	catch(const InvalidArticleError & iae) {
		(*clt->co) << "441 invalid article\r\n";
		return -1;
	}
	catch(const NoSuchFieldError & nsfe) {
		(*clt->co) << "441 invalid article\r\n";
		return -1;
	}

	try {
		// client is authorized to post the article
		clt->srvr->post(&art);
		(*clt->co) << "240 Article posted\r\n";
		return 0;
	}
	catch(const InvalidArticleError & iae) {
		(*clt->co) << "441 invalid article\r\n";
		return -1;
	}
	catch(const NotAllowedError & nae) {
		// (440 is the reply to POST itself, before the article)
		(*clt->co) << "441 posting not allowed\r\n";
		return -1;
	}
	catch(const Error & e) {
		// e.g. the article can't be spooled for later; 449 isn't an
		// NNTP reply
		(*clt->co) << "441 posting failed\r\n";
		return -1;
	}
}

int ns_quit(ClientData * clt, int argc, char *argv[])
{
	(*clt->co) << "205 Good bye\r\n";
	return 1;
}


/*
 * 221 Header follows
 * 412 No news group current selected
 * 420 No current article selected
 * 430 no such article
 * 502 no permission
 */
/*
 * Reads an article range: n, n- or n-m (RFC 3977); a range must start
 * with a number. Returns 0 for a syntax error.
 */
static int parse_range(const char *s, artnum_t * fst, artnum_t * lst)
{
	const char *p;

	if (!parse_artnum(s, fst, &p))
		return 0;
	*lst = *fst;
	if (*p == '\0')
		return 1;
	if (*p != '-')
		return 0;
	*lst = ARTNUM_MAX;
	p++;
	if (isdigit((unsigned char) *p) && !parse_artnum(p, lst, &p))
		return 0;
	return *p == '\0';
}

/*
 * Selects the client's current group for XOVER/XHDR/XPAT. Returns 0, or
 * -1 after an error reply. When the news server or the spool fails, the
 * reply is 403 (RFC 3977 3.2.1), as for ARTICLE, STAT, NEXT/LAST and
 * LISTGROUP; 412 means that no group is selected, or that the current
 * one no longer exists (RFC 3977 6.2.1.2; it was 411, which is for a
 * group named in the command).
 */
static int current_group(ClientData * clt)
{
	try {
		if (!clt->grp)
			clt->grp = clt->srvr->getgroup(clt->groupname);
	}
	catch(const NoSuchGroupError & nsge) {
		(*clt->co) << "412 current group no longer exists\r\n";
		return -1;
	}
	catch(const Error & e) {
		e.print();
		(*clt->co) << "403 operation failed\r\n";
		return -1;
	}
	ASSERT2(clt->grp->testdb());
	return 0;
}

/*
 * Sends the status line to out before the first character that is
 * written; used() tells whether anything was. OVER and HDR use this to
 * answer 423 instead of an empty list (RFC 3977 8.3 and 8.5).
 */
class LazyStatus:public std::streambuf {
	ostream & out;
	string status;
	bool started;
	void start(void) {
		if (!started) {
			out << status;
			started = true;
		}
	}
      protected:
	virtual int overflow(int c) {
		if (c != EOF) {
			start();
			out.put((char) c);
			if (!out.good())
				return EOF;
		}
		return c;
	}
	virtual std::streamsize xsputn(const char *p, std::streamsize n) {
		if (n > 0) {
			start();
			out.write(p, n);
			if (!out.good())
				return 0;
		}
		return n;
	}
	virtual int sync(void) {
		out.flush();
		return out.good() ? 0 : -1;
	}
      public:
	LazyStatus(ostream & o, const char *st)
	:out(o), status(st), started(false) {
	}
	bool used(void) const {
		return started;
	}
};

int ns_xover(ClientData * clt, int argc, char *argv[])
{
	int i = 1;
	artnum_t fst, lst;

	// OVER does not advertise MSGID; RFC 3977 8.3.2 requires 503 for
	// that form, independently of the currently selected group.
	if (strcmp(argv[0], "over") == 0 && argc == 2 && argv[1][0] == '<') {
		(*clt->co) << "503 OVER by message-id is not supported\r\n";
		return -1;
	}

	if (strcmp(argv[0], "xhdr") == 0) {
		i = 2;
		if (argc < 2) {
			(*clt->co) << "501 Syntax: xhdr header [range|<id>]\r\n";
			return -1;
		}
		// XHDR header <message-id> (RFC 2980 2.6): needs no group
		if (argc == 3 && argv[2][0] == '<') {
			Article a;
			if (fetch_readable(clt, argv[2], a) < 0)
				return -1;
			string val;
			try {
				val = a.getfield(Newsgroup::headerfield(argv[1]).
						 c_str(), 0);
			}
			catch(const NoSuchFieldError & nsfe) {
				val = "(none)";
				// no Lines header: the body lines, as the
				// overview has them (XHDR by number)
				if (strcasecmp(Newsgroup::headerfield(argv[1]).
					       c_str(), "lines:") == 0) {
					char n[32];
					snprintf(n, sizeof(n), "%lu",
						 a.bodylines());
					val = n;
				}
			}
			(*clt->co) << "221 " << argv[1] << " header follows\r\n"
			    << argv[2] << " " << val << "\r\n.\r\n";
			return 0;
		}
	}
	if (clt->groupname[0] == '\0') {
		(*clt->co) << "412 No newsgroup currently selected\r\n";
		return -1;
	}
	switch (argc - i) {
	case 0:
		// the current article (RFC 3977 8.3.2, 8.5.2: 420 without
		// one; it answered an empty list)
		if (clt->nbr == 0) {
			(*clt->co) <<
			    "420 no current article has been selected\r\n";
			return -1;
		}
		fst = lst = clt->nbr;
		break;
	case 1:
		if (parse_range(argv[i], &fst, &lst))
			break;
		// fall through
	default:
		(*clt->co) << "501 Syntax: xover [range]\r\n";
		return -1;
	}
	if (current_group(clt) < 0)
		return -1;

	switch (i) {
	case 1:
		if (strcmp(argv[0], "over") == 0) {
			// Do not send 224 until an overview record is available.
			// A reversed range has no articles (RFC 3977 8.3.2).
			LazyStatus ls(*clt->co, "224 Overview information follows\r\n");
			ostream os(&ls);
			if (fst <= lst)
				clt->grp->printoverdb(os, fst, lst);
			os.flush();
			if (!ls.used()) {
				if (argc == 1)
					(*clt->co) << "420 no current article has been selected\r\n";
				else
					(*clt->co) << "423 no articles in that range\r\n";
				return 0;
			}
		} else {
			// XOVER keeps its existing 224/empty-list behaviour.
			(*clt->co) << "224 Overview information follows\r\n";
			clt->grp->printoverdb(*clt->co, fst, lst);
		}
		break;
	case 2:
		(*clt->co) << "221 " << argv[1] << " header follows [" << fst
			   << "-" << lst << "]\r\n";
		clt->grp->printheaderdb(*clt->co, argv[1], fst, lst);
		break;
	}
	(*clt->co) << ".\r\n";
	return 0;
}

/*
 * XPAT header range|<message-id> pattern [pattern ...] (RFC 2980 2.9):
 * the lines of XHDR whose header value matches the wildmat pattern;
 * more than one pattern argument are joined with spaces into one. Like
 * XHDR, it sees the overview fields, and every header of the articles
 * in the spool.
 */
int ns_xpat(ClientData * clt, int argc, char *argv[])
{
	artnum_t fst, lst;
	string pat;

	if (argc < 4) {
		(*clt->co) <<
		    "501 Syntax: xpat header range|<id> pattern [pattern ...]\r\n";
		return -1;
	}
	for (int i = 3; i < argc; i++) {
		if (i > 3)
			pat += ' ';
		pat += argv[i];
	}

	if (argv[2][0] == '<') {
		Article a;
		// (this didn't check the Read list: any article's header)
		if (fetch_readable(clt, argv[2], a) < 0)
			return -1;
		(*clt->co) << "221 " << argv[1] << " matches follow\r\n";
		try {
			string fld =
			    a.getfield(Newsgroup::headerfield(argv[1]).c_str(), 0);
			if (Newsgroup::headermatch(fld, pat.c_str()))
				(*clt->co) << argv[2] << " " << fld << "\r\n";
		}
		catch(const NoSuchFieldError & nsfe) {
		}
		(*clt->co) << ".\r\n";
		return 0;
	}

	if (clt->groupname[0] == '\0') {
		(*clt->co) << "412 No newsgroup currently selected\r\n";
		return -1;
	}
	if (!parse_range(argv[2], &fst, &lst)) {
		(*clt->co) <<
		    "501 Syntax: xpat header range|<id> pattern [pattern ...]\r\n";
		return -1;
	}
	if (current_group(clt) < 0)
		return -1;
	(*clt->co) << "221 " << argv[1] << " matches follow [" << fst
		   << "-" << lst << "]\r\n";
	clt->grp->printheaderdb(*clt->co, argv[1], fst, lst, pat.c_str());
	(*clt->co) << ".\r\n";
	return 0;
}

/*
 * HDR field [range|<message-id>] (RFC 3977 8.5). By number it reads the
 * overview fields (see LIST HEADERS); by message-ID it fetches the
 * article, so every header is there.
 */
int ns_hdr(ClientData * clt, int argc, char *argv[])
{
	artnum_t fst, lst;

	if (argc < 2 || argc > 3) {
		(*clt->co) << "501 Syntax: hdr field [range|<id>]\r\n";
		return -1;
	}

	if (argc == 3 && argv[2][0] == '<') {
		Article a;
		string val;
		if (fetch_readable(clt, argv[2], a) < 0)
			return -1;
		if (strcasecmp(argv[1], ":bytes") == 0) {
			val = a.getfield("bytes:");
		} else if (argv[1][0] == ':') {
			(*clt->co) << "503 " << argv[1] <<
			    " not available by message-id\r\n";
			return -1;
		} else {
			try {
				val = a.getfield(Newsgroup::
						 headerfield(argv[1]).c_str(), 0);
			}
			catch(const NoSuchFieldError & nsfe) {
			}
		}
		(*clt->co) << "225 Headers follow\r\n0 " << val << "\r\n.\r\n";
		return 0;
	}

	if (clt->groupname[0] == '\0') {
		(*clt->co) << "412 No newsgroup currently selected\r\n";
		return -1;
	}
	if (argc == 3) {
		if (!parse_range(argv[2], &fst, &lst)) {
			(*clt->co) << "501 Syntax: hdr field [range|<id>]\r\n";
			return -1;
		}
	} else {
		if (clt->nbr == 0) {
			(*clt->co) <<
			    "420 no current article has been selected\r\n";
			return -1;
		}
		fst = lst = clt->nbr;
	}
	string fld = hdr_overview_field(clt->srvr->overviewfmt(), argv[1]);
	if (fld.empty()) {
		(*clt->co) << "503 " << argv[1] <<
		    " not available by article number (see LIST HEADERS)\r\n";
		return -1;
	}
	if (current_group(clt) < 0)
		return -1;

	LazyStatus ls(*clt->co, "225 Headers follow\r\n");
	ostream os(&ls);
	clt->grp->printheaderdb(os, fld.c_str(), fst, lst, Newsgroup::hdr_all);
	os.flush();
	if (ls.used())
		(*clt->co) << ".\r\n";
	else
		(*clt->co) << "423 no articles in that range\r\n";
	return 0;
}

// Sends text, whose lines end with "\n", as the lines of a multi-line
// reply: with CRLF and dot-stuffed (RFC 3977 3.1.1). XDEBUG's dumps
// come from printers that write endl, which went out as bare LF.
static void send_text_lines(std::ostream & os, const std::string & text)
{
	std::string::size_type b = 0;
	while (b < text.size()) {
		std::string::size_type e = text.find('\n', b);
		if (e == std::string::npos)
			e = text.size();
		std::string l = text.substr(b, e - b);
		if (!l.empty() && l[l.size() - 1] == '\r')
			l.erase(l.size() - 1);
		if (!l.empty() && l[0] == '.')
			os << '.';
		os << l << "\r\n";
		b = e + 1;
	}
}

// The replies are multi-line ones with the informative code 100: they
// had the code "xxx" (help none), and an unknown "xdebug dump" argument
// got no reply at all.
int ns_xdebug(ClientData * clt, int argc, char *argv[])
{
	if (argc == 3 && strcmp(argv[1], "dump") == 0) {
		if (strcmp(argv[2], "authorization") == 0) {
			ostringstream text;
			text << *clt->access_entry << "\n";
			(*clt->co) << "100 authorization data follows\r\n";
			send_text_lines(*clt->co, text.str());
			(*clt->co) << ".\r\n";
			return 0;
		}
		if (strcmp(argv[2], "configuration") == 0) {
			ostringstream text;
			Cfg.printParameters (&text);
			(*clt->co) << "100 configuration data follows\r\n";
			send_text_lines(*clt->co, text.str());
			(*clt->co) << ".\r\n";
			return 0;
		}
	}

	if (argc == 2) {
		if (strcmp(argv[1], "help") == 0) {
			(*clt->co) << "100 Valid commands:\r\n";
			(*clt->co) << "xdebug dump authorization:\r\n";
			(*clt->co) << "xdebug dump configuration:\r\n";
			(*clt->co) << ".\r\n";
			return 0;
		}
	}

	(*clt->co) << "501 Syntax: xdebug help\r\n";
	return -1;
}

/**
 * \author Christof Meerwald
 * Capabilities, see RFC 3977.
 */
int ns_capabilities(ClientData * clt, int argc, char *argv[])
{
	// What this client can do now (RFC 3977 3.3.1, 5.2): the reading
	// commands and POST only with those rights and not before a
	// required login, AUTHINFO only before a login (RFC 4643 2.2).
	unsigned int rights = current_rights(clt);

	(*clt->co) << "101 Capability list:\r\n";
	(*clt->co) << "VERSION 2\r\n";
	(*clt->co) << "IMPLEMENTATION " PACKAGE " " VERSION "\r\n";
	if (clt->access_entry->authentication.getType() != "none" &&
	    clt->auth_state != AUTH_OK) {
		// without USER where AUTHINFO USER would get 483 (RFC 4643
		// 2.1: it is not available now)
		if (clt->access_entry->auth_requires_tls && !clt->tls)
			(*clt->co) << "AUTHINFO\r\n";
		else
			(*clt->co) << "AUTHINFO USER\r\n";
	}
	if (rights & AccessEntry::af_read) {
		(*clt->co) << "READER\r\n";
		(*clt->co) << "HDR\r\n";
		(*clt->co) << "OVER\r\n";
		(*clt->co) <<
		    "LIST ACTIVE ACTIVE.TIMES HEADERS NEWSGROUPS OVERVIEW.FMT\r\n";
	}
	if (rights & AccessEntry::af_post) {
		(*clt->co) << "POST\r\n";
	}
	(*clt->co) << ".\r\n";
	return 0;
}


#ifdef HAVE_LIBWRAP
#ifndef WITH_SYSLOG
/* This is from my syslog.h */
#define LOG_EMERG       0	/* system is unusable */
#define LOG_ALERT       1	/* action must be taken immediately */
#define LOG_CRIT        2	/* critical conditions */
#define LOG_ERR         3	/* error conditions */
#define LOG_WARNING     4	/* warning conditions */
#define LOG_NOTICE      5	/* normal but significant condition */
#define LOG_INFO        6	/* informational */
#define LOG_DEBUG       7	/* debug-level messages */
#endif

/* needed for TCP wrappers */
int allow_severity = LOG_INFO;
int deny_severity = LOG_NOTICE;
#endif
void set_client_command_table (ClientData &clt)
{
	clt.client_command_map.enableAll();
	if (!(clt.access_entry->access_flags & AccessEntry::af_read)) {
		clt.client_command_map.disableRead();
	}

	if (!(clt.access_entry->access_flags & AccessEntry::af_post)) {
		clt.client_command_map.disablePost ();
	}

	if (!(clt.access_entry->access_flags & AccessEntry::af_debug)) {
		clt.client_command_map.disableDebug ();
	}
}

/* nnrpd(fd)
 */
/*
 * Passes the client's output on to its stream and remembers what the
 * current reply has written so far: the command loop needs it when a
 * command runs into a corrupt spool database.
 */
class ReplyTracker:public std::streambuf {
	std::streambuf *dst;
	void note(const char *p, std::streamsize n) {
		for (std::streamsize i = 0; i < n && count + i < 3; i++) {
			head[count + i] = p[i];
			head[count + i + 1] = '\0';
		}
		if (n > 0)
			last = p[n - 1];
		count += n;
	}
      protected:
	virtual int overflow(int c) {
		if (c == EOF)
			return 0;
		char ch = (char) c;
		if (dst->sputc(ch) == EOF)
			return EOF;
		note(&ch, 1);
		return c;
	}
	virtual std::streamsize xsputn(const char *p, std::streamsize n) {
		std::streamsize w = dst->sputn(p, n);
		if (w > 0)
			note(p, w);
		return w;
	}
	virtual int sync(void) {
		return dst->pubsync();
	}
      public:
	unsigned long count;	// characters since mark()
	char head[4];		// the first three of them
	char last;		// the last one
	ReplyTracker(std::streambuf * d)
	:dst(d), count(0), last('\n') {
		head[0] = '\0';
	}
	void mark(void) {
		count = 0;
		head[0] = '\0';
	}
};

// Whether the reply that begins with status (its first three
// characters) to command cmd is a multi-line one.
static bool multiline_reply(const char *status, const char *cmd)
{
	static const char *const codes[] = {
		"100", "101", "215", "220", "221", "222", "224", "225",
		"230", "231", "282", NULL
	};
	if (strcmp(status, "211") == 0)
		return strcasecmp(cmd, "listgroup") == 0;
	for (int i = 0; codes[i]; i++)
		if (strcmp(status, codes[i]) == 0)
			return true;
	return false;
}

/*
 * A command failed with an exception after the reply had written what
 * tracker saw: finish the reply so that the client stays in step. With
 * nothing written yet, the reply is a 403 (RFC 3977 3.2.1: internal
 * fault; 503 is "feature not supported") with what.
 */
static void finish_reply(ostream & co, const ReplyTracker & tracker,
			 const char *cmd, const char *what)
{
	if (tracker.count == 0) {
		co << "403 " << what << "\r\n";
	} else if (multiline_reply(tracker.head, cmd)) {
		// a list had begun: end it
		if (tracker.last != '\n')
			co << "\r\n";
		co << ".\r\n";
	} else if (strcmp(tracker.head, "340") == 0) {
		co << "441 posting failed\r\n";
	}
}

/*
 * Sets the send timeout of the client's output and the receive timeout of
 * its input from ClientTimeout (not for a client that isn't a socket).
 * A send or receive that times out fails; the stream goes bad and the
 * session ends.
 */
static void set_socket_timeouts(int in_fd, int out_fd, int rcv, int snd)
{
	struct timeval tv;
	tv.tv_usec = 0;
	tv.tv_sec = snd;
	setsockopt(out_fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));
	tv.tv_sec = rcv;
	setsockopt(in_fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
}

static void set_client_timeouts(int in_fd, int out_fd)
{
	// (0: no timeout; set anyway, the TLS handshake had its own)
	if (Cfg.ClientTimeout <= 0)
		set_socket_timeouts(in_fd, out_fd, 0, 0);
	else
		set_socket_timeouts(in_fd, out_fd, Cfg.ClientTimeout + 10,
				    Cfg.ClientTimeout);
}

// Seconds a client's TLS handshake may take in all: ClientTimeout, at most
// 30, also when ClientTimeout is 0 (no limit for commands). Each session is
// a process counted in MaxConnections, and a client that never finishes
// the handshake must not keep one for long.
static int tls_handshake_timeout(void)
{
	return (Cfg.ClientTimeout > 0 && Cfg.ClientTimeout < 30)
	    ? Cfg.ClientTimeout : 30;
}

void nnrpd(int fd, bool tls)
{
	ClientData clt;
	char req[1024], oreq[1024], *rp;
	char *argv[256];
	int argc;
	bool too_long;
	const char *cmd;
	map < string, nnrp_command_t * >::iterator cmdp, end =
	    nnrp_commands.end();
	int errc = 0;
	// (with tls the server side of a TLS connection, see accept_tls)
	sockstream sock_stream(tls, true);
	int nice;
#ifdef HAVE_LIBWRAP
	bool have_peer = false;	// the client has an address (for libwrap)
#endif

	/* set nice value for master server */
#ifdef HAVE_SETPRIORITY
	errno = 0;
	nice = Cfg.NiceServer;
	nice += getpriority(PRIO_PROCESS, 0);
	if (nice == -1 && errno != 0) {
		slog.
		    p(Logger::
		      Error) << "getpriority failed: " << strerror(errno)
		    << "\n";
	} else {
		if (setpriority(PRIO_PROCESS, 0, nice) == -1)
			slog.
			    p(Logger::
			      Error) << "setpriority failed: " <<
			    strerror(errno) << "\n";
	}
#endif

	if (fd >= 0) {
		sock_stream.attach(fd);

		sock_stream.setkeepalive();
		sock_stream.setnodelay();

		clt.co = &sock_stream;
		clt.ci = &sock_stream;

		// get network address and name of client
		clt.socklen = sizeof(clt.sock);
		if (getpeername
		    (fd, (struct sockaddr *) &clt.sock,
		     &clt.socklen) < 0) {
			// Cannot get socket --- connection from stdin?
			sprintf(clt.client_name, "fd#%d", fd);
			clt.client_logname = clt.client_name;
		} else {
#ifdef HAVE_LIBWRAP
			have_peer = true;
#endif
			// An IPv4 client of the dual-stack socket (ListenTo DEFAULT)
			// arrives as ::ffff:a.b.c.d; use its plain IPv4 address, so
			// logs, NNTP-Posting-Host and libwrap see a.b.c.d.
			if ((clt.sock.ss_family == AF_INET6) &&
			    IN6_IS_ADDR_V4MAPPED(&((const struct sockaddr_in6 *) &clt.sock)->sin6_addr)) {
				struct sockaddr_in6 in6;
				struct sockaddr_in in4;

				memcpy(&in6, &clt.sock, sizeof(in6));
				memset(&in4, 0, sizeof(in4));
				in4.sin_family = AF_INET;
#ifdef SIN6_LEN
				in4.sin_len = sizeof(in4);
#endif
				in4.sin_port = in6.sin6_port;
				memcpy(&in4.sin_addr, &in6.sin6_addr.s6_addr[12], 4);
				memset(&clt.sock, 0, sizeof(clt.sock));
				memcpy(&clt.sock, &in4, sizeof(in4));
				clt.socklen = sizeof(in4);
			}

			// get the name of the client and store it in clt.client_name.
			// if the client does not have a name, use the ip-address instead
			// store it in nntp_posting_host as used by libnserver
			if (getnameinfo((const struct sockaddr *) &clt.sock,
					clt.socklen,
					clt.client_name, sizeof(clt.client_name),
					NULL, 0, 0) != 0)
				clt.client_name[0] = '\0';

			struct addrinfo hints;
			memset(&hints, 0, sizeof(hints));
			hints.ai_flags =
			  (clt.sock.ss_family == AF_INET6) ? AI_V4MAPPED : 0;
			hints.ai_family = clt.sock.ss_family;
			hints.ai_socktype = SOCK_STREAM;
			hints.ai_protocol = IPPROTO_TCP;

			struct addrinfo *res = NULL;
			getaddrinfo(clt.client_name, NULL, &hints, &res);

			// have to verify the address we got from getnameinfo
			bool found_addr = false;
			struct addrinfo *addr = res;
			while (addr != NULL) {
				if (addr->ai_addr->sa_family == clt.sock.ss_family) {
					if (addr->ai_addr->sa_family == AF_INET) {
						if (((const struct sockaddr_in *) addr->ai_addr)->sin_addr.s_addr ==
						    ((const struct sockaddr_in *) &clt.sock)->sin_addr.s_addr) {
							found_addr = true;
							break;
						}
					} else if (addr->ai_addr->sa_family == AF_INET6) {
						if (!memcmp(((const struct sockaddr_in6 *) addr->ai_addr)->sin6_addr.s6_addr,
							    ((const struct sockaddr_in6 *) &clt.sock)->sin6_addr.s6_addr,
							    16)) {
							found_addr = true;
							break;
						}
					}
				}

				addr = addr->ai_next;
			}
			freeaddrinfo(res);

			if (!found_addr) {
				clt.client_name[0] = '\0';
			}

			clt.client_addr[0] = '\0';
			getnameinfo((const struct sockaddr *) &clt.sock,
				    clt.socklen,
				    clt.client_addr, sizeof(clt.client_addr),
				    NULL, 0, NI_NUMERICHOST);

			if (clt.client_name[0] == '\0') {
				strcpy(clt.client_name, clt.client_addr);
			}
			strcpy(nntp_posting_host, clt.client_name);

			// add the client's name to its logging name (clt.client_logname),
			// if names should be logged
			if (Cfg.LogStyle & Config::LogName) {
				clt.client_logname = clt.client_name;
			}

			if (Cfg.LogStyle & Config::LogAddr) {
				if (clt.client_logname.length() > 0)
					clt.client_logname += " [";
				else
					clt.client_logname += '[';
				clt.client_logname += clt.client_addr;
				clt.client_logname += ']';
			}
		}

		// The TLS handshake, before anything is written to the client
		// (also the 502 of libwrap) and before anything is read: all
		// input goes through TLS. Its own timeouts, see
		// tls_handshake_timeout; ClientTimeout's are set below.
		if (tls) {
			const int hs = tls_handshake_timeout();
			set_socket_timeouts(fd, fd, hs, hs);
			string info;
			if (!sock_stream.accept_tls(hs, info)) {
				slog.p(Logger::Notice) << clt.client_logname
				    << " TLS handshake failed: " << info << "\n";
				return;
			}
			clt.tls = true;
			slog.p(Logger::Info) << clt.client_logname
			    << " TLS " << info << "\n";
		}

		// check whether the client is allowed access according to
		// libwrap
#ifdef HAVE_LIBWRAP
		// (hosts_ctl takes char *, not string literals)
		static char libwrap_daemon[] = PACKAGE;
		static char libwrap_unknown[] = STRING_UNKNOWN;
		// Check the hosts_access configuration; emulate INN error message
		if (have_peer &&
		    !hosts_ctl(libwrap_daemon,
			       clt.client_name, clt.client_addr,
			       libwrap_unknown)) {
			slog.p(Logger::Notice) << clt.
			    client_logname << " denied - hosts.allow\n";
			(*clt.
			 co) <<
		       "502 You have no permission to talk.  Goodbye.\r\n";
			clt.co->flush ();
			return;
		}
#endif
	} else {
		cin.unsetf(ios::skipws);
		clt.ci = &cin;
		clt.co = &cout;
		strcpy(clt.client_name, "stdin");
		clt.client_logname = clt.client_name;
		fd = 0;
	}

	// ClientTimeout also limits each send to the client and each read
	// inside a command (the article of a POST); it used to cover only
	// the wait for the next command. A client that stopped reading left
	// its session blocked in send(), and XOVER, XHDR, HDR and LISTGROUP
	// write while they hold the group's lock: every other session on the
	// group hung until the client went away. The read timeout is a little
	// longer than ClientTimeout, so that the wait for a command still
	// ends with the alarm and its "400 ... timed out" reply.
	set_client_timeouts(fd, clt.ci == &cin ? 1 : fd);
	if (clt.tls) {
		// a TLS read gives up when no data came within the receive
		// timeout; a write 1 s after send() timed out (SO_SNDTIMEO,
		// ClientTimeout), so about where a plain send() fails. Not 0:
		// that would be the I/O timeout, i.e. no limit.
		if (Cfg.ClientTimeout > 0)
			sock_stream.set_wait_limits(Cfg.ClientTimeout + 10, 1);
		else
			sock_stream.set_wait_limits(0, 0);
	}

#ifdef NO_STREAM_BUFFERING
	{
		streambuf *b;
		b = clt.ci->rdbuf();
		if (b)
			b->setbuf(0, 0);
		b = clt.co->rdbuf();
		if (b)
			b->setbuf(0, 0);
	}
#endif

	// What each reply has written so far (see the command loop)
	ReplyTracker tracker(clt.co->rdbuf());
	ostream tracked(&tracker);
	clt.co = &tracked;

	// check whether the client is allowed access according to
	// our own access configuration
	clt.access_entry =
		Cfg.client(clt.client_name,
			   (struct sockaddr *) &clt.sock, clt.socklen);
	if (!clt.access_entry || !clt.access_entry->access_flags) {
//   nnrpd_deny:
		slog.p(Logger::Notice) << clt.
		    client_logname << " denied\n";
		(*clt.
		 co) << "502 You have no permission to talk.  Goodbye.\r\n";
		clt.co->flush();
		return;
	} else {
		slog.p(Logger::Debug) << "nnrpd: access_entry name matched: "
			<< clt.access_entry->hostname << "\n";
	}

	slog.p(Logger::Notice) << clt.client_logname << " connect\n";

	if (clt.access_entry->authentication.getType() != "none") {
		clt.auth_state = AUTH_REQUIRED;
	}
	if (clt.access_entry->authentication.getType() == "unix" &&
	    clt.access_entry->authentication.getNrOfFields() > 1) {
		// authentication optional, not required
		clt.auth_state = AUTH_NOT_REQUIRED;
	}
	if (clt.access_entry->authentication.getType() == "file") {
		// authentication optional, not required
		clt.auth_state = AUTH_NOT_REQUIRED;
	}
	if (clt.access_entry->authentication.getType() == "pam") {
		// authentication optional, not required
		clt.auth_state = AUTH_NOT_REQUIRED;
	}
	if (clt.access_entry->authentication.getType() == "pam+file") {
		// authentication optional, not required
		clt.auth_state = AUTH_NOT_REQUIRED;
	}

	// Set client command table, can be changed after successfully
	// authentication!
	set_client_command_table (clt);

	// (after setugid: .history belongs to the news user). Without
	// "History on" every History call does nothing.
	if (Cfg.HistoryEnabled)
		History::setspooldir(Cfg.SpoolDirectory);
	try {
		clt.srvr = new CServer(Cfg.SpoolDirectory, &(Cfg.srvrs));
		clt.srvr->setttl(Cfg.ttl_list, Cfg.ttl_desc);
		clt.srvr->setmaxarticlesize(Cfg.MaxArticleSize);
	}
	catch(const SystemError & se) {
		slog.
		    p(Logger::
		      Alert) << "CServer failed, check permissions\n";
		se.print();
		(*clt.
		 co) << "400 " PACKAGE " " VERSION
	       ", service not available\r\n";
		// exit() doesn't flush the socket stream (it does stdout in
		// inetd mode): without this the client got no reply
		clt.co->flush();
		exit(1);
	}

	send_ready(&clt);
	do {
		// getline writes only the current line and its terminator; clear
		// the old request first so a shorter line cannot leave an AUTHINFO
		// password behind in the reused buffer.
		memset(req, 0, sizeof(req));
		flush(*clt.co);
		if (!clt.co->good()) {
			// the client doesn't read its replies (send timed out)
			// or has gone
			slog.p(Logger::Notice) << clt.client_logname
			    << " can't be written to, session ended\n";
			break;
		}

		alarmed = 0;
		// (no wait after a signal during the last command)
		too_long = false;
		if (Xsignal == -1) {
			alarm (Cfg.ClientTimeout);
			clt.ci->getline(req, sizeof(req), '\n');
			if (clt.ci->fail() && !clt.ci->bad() && !clt.ci->eof() &&
			    clt.ci->gcount() == (std::streamsize) sizeof(req) - 1) {
				// The line doesn't fit into req: skip the rest of
				// it and answer 501 (RFC 3977 3.2.1). The failed
				// getline used to end the session without a reply.
				too_long = true;
				clt.ci->clear();
				clt.ci->ignore(std::numeric_limits < std::streamsize >::max(),
					       '\n');
			}
			alarm (0);
		}
		if (Xsignal != -1) {
			alarmed = 0;	// (else the reply isn't sent)
			(*clt.co) << "400 " PACKAGE " " VERSION ", service discontinued\r\n";
			slog.p(Logger::Notice) << clt.client_logname << " discontinued due to Signal\n";
			goto client_exit;
		}
		if (alarmed) {
			alarmed = 0;
			(*clt.co) << "400 " PACKAGE " " VERSION ", service timed out!\r\n";
			slog.p(Logger::Notice) << clt.client_logname << " ClientTimeout reached\n";
			goto client_exit;
		}
		// gcount includes a discarded newline only when getline found it.
		// At EOF it counts just the stored bytes, including a trailing NUL.
		if (!too_long && !clt.ci->bad() && clt.ci->gcount() > 0) {
			std::streamsize n = clt.ci->gcount() - (clt.ci->eof() ? 0 : 1);
			if (memchr(req, '\0', (size_t) n) != NULL) {
				(*clt.co) << "501 NUL in command\r\n";
				if (clt.ci->eof())
					break;
				continue;
			}
		}
		if (clt.ci->eof()) {
			slog.p(Logger::Debug) << "NewsCache.cc input stream eof!\n";
			break;
		}
		if (!clt.ci->good()) {
			slog.p(Logger::Debug) << "NewsCache.cc input stream not good!\n";
			break;
		}
		if (too_long) {
			// (not logged: it may be an AUTHINFO line)
			slog.p(Logger::Notice) << clt.client_logname
			    << " command line too long\n";
			(*clt.co) << "501 Line too long\r\n";
			continue;
		}
		rp = req + strlen(req);
		while (rp > req) {
			rp--;
			if (!isspace(*rp))
				break;
			else
				*rp = '\0';
		}
		// oreq, for the log, without leading blanks either: the split
		// below skips them. AUTHINFO and malformed auth* commands can
		// contain a password, so retain only their command word.
		rp = req;
		while (isspace((unsigned char)*rp))
			rp++;
		if (!strncasecmp(rp, "auth", 4)) {
			char *cmd_end = rp;
			while (*cmd_end && !isspace((unsigned char)*cmd_end))
				cmd_end++;
			size_t cmd_len = (size_t) (cmd_end - rp);
			memcpy(oreq, rp, cmd_len);
			oreq[cmd_len] = '\0';
		} else
			strcpy(oreq, rp);
		if (strncasecmp(oreq, "authinfo", 8))
			slog.p(Logger::Info) << clt.
			    client_logname << " " << oreq << "\n";
		else
			slog.p(Logger::Info) << clt.
			    client_logname << " authinfo\n";

		// Split command into arguments
		for (rp = req, argc = 0; *rp && argc < 256; rp++) {
			if (isspace(*rp)) {
				*rp = '\0';
			} else {
				if (rp == req || *(rp - 1) == '\0')
					argv[argc++] = rp;
				if (argc == 1)
					*rp = tolower(*rp);
			}
		}
		if (!argc)
			continue;
		if (argc == 256) {
			(*clt.co) << "501 Line too long\r\n";
			continue;
		}
		// Call function for command
		cmd = argv[0];
		// Before the login: CAPABILITIES may come at any time, and
		// lists AUTHINFO USER (RFC 3977 5.2, RFC 4643 2.2); MODE
		// READER comes before the login (RFC 4643 2.2); unknown
		// commands get 500 below.
		if (clt.auth_state == AUTH_REQUIRED &&
		    strcasecmp(cmd, "authinfo") != 0 &&
		    strcasecmp(cmd, "quit") != 0 &&
		    strcasecmp(cmd, "help") != 0 &&
		    strcasecmp(cmd, "capabilities") != 0 &&
		    strcasecmp(cmd, "mode") != 0 &&
		    nnrp_commands.find(argv[0]) != end) {
			(*clt.
			 co) << "480 Authentication required (" << cmd <<
		       ")\r\n";
			continue;
		}

		cmdp = clt.client_command_map.find(argv[0]);
		if (cmdp == clt.client_command_map.end() &&
		    clt.auth_state != AUTH_OK &&
		    nnrp_commands.find(argv[0]) != end) {
			// A known command this client may not use yet, but a
			// login could allow it (RFC 4643: 480, so that
			// newsreaders offer a login).
			(*clt.co) << "480 Authentication required (" << cmd <<
			    ")\r\n";
			continue;
		}
		if (cmdp == clt.client_command_map.end() &&
		    nnrp_commands.find(argv[0]) != end) {
			// A known command that the client's entry doesn't
			// allow, and no login can add it: 440 for POST
			// (RFC 3977 6.3.1), 502 otherwise (3.2.1). It got
			// "500 What?", as if the command didn't exist.
			slog.p(Logger::Notice) << clt.client_logname
			    << " not allowed " << cmd << "\n";
			if (!strcasecmp(cmd, "post"))
				(*clt.co) << "440 posting not permitted\r\n";
			else
				(*clt.co) << "502 access denied (" << cmd <<
				    ")\r\n";
			continue;
		}
		if (cmdp == clt.client_command_map.end() || !cmdp->second->func) {
			slog.p(Logger::Notice) << clt.client_logname
			    << " unrecognized " << oreq << "\n";
			(*clt.co) << "500 What?\r\n";
			continue;
		}

		// Validate client-supplied Message-IDs before any command can
		// consult the spool or contact an upstream. Numeric arguments
		// remain with each command's existing range/number parser.
		int idarg = 0;
		if (argc == 2 && (!strcmp(cmd, "article") || !strcmp(cmd, "head") ||
		    !strcmp(cmd, "body") || !strcmp(cmd, "stat") || !strcmp(cmd, "over") ||
		    !strcmp(cmd, "xover")))
			idarg = 1;
		else if (argc == 3 && (!strcmp(cmd, "hdr") || !strcmp(cmd, "xhdr")))
			idarg = 2;
		else if (argc >= 4 && !strcmp(cmd, "xpat"))
			idarg = 2;
		if (idarg && (!isdigit((unsigned char) argv[idarg][0]) ||
		    strpbrk(argv[idarg], "<>@")) && !valid_msgid(argv[idarg])) {
			(*clt.co) << "501 invalid message-id\r\n";
			continue;
		}

		for (int attempt = 0;; attempt++) {
			tracker.mark();
			try {
				errc = cmdp->second->func(&clt, argc, argv);
				break;
			}
			catch(const CorruptDBError & e) {
				// NVcontainer has logged it, moved the file aside
				// and started a new one. The current group may
				// hold what is left of the old one: drop it.
				if (clt.grp) {
					clt.srvr->freegroup(clt.grp);
					clt.grp = NULL;
				}
				errc = -1;
				// nothing sent yet: once more, on the new
				// database (it fills itself from upstream)
				if (tracker.count == 0 && attempt == 0 &&
				    strcasecmp(cmd, "post") != 0)
					continue;
				finish_reply(*clt.co, tracker, cmd,
					     "spool database damaged, try again");
				break;
			}
			catch(const Error & e) {
				// Anything else a command didn't handle (e.g. a
				// stored article that doesn't parse): the client
				// gets an answer and the session goes on. (It
				// ended the session, in inetd mode with SIGABRT.)
				slog.p(Logger::Error) << clt.client_logname
				    << " " << cmd << " failed: " << e._errtext
				    << "\n";
				errc = -1;
				finish_reply(*clt.co, tracker, cmd,
					     "internal error");
				break;
			}
		}
		if (!(Cfg.LogStyle & Config::LogINN)) {
			if (errc < 0) {
				if (strncasecmp(oreq, "authinfo", 8))
					slog.p(Logger::Notice) << clt.
							client_logname << " failed " << oreq <<
							"\n";
				else if (clt.auth_refused)
					// (the user name only when its password was
					// checked: not for 482, 501, 502 or too long)
					slog.p(Logger::Notice) << clt.
						client_logname << " failed authinfo for user  " <<
						clt.auth_user << "\n";
				else
					slog.p(Logger::Notice) << clt.
						client_logname << " failed authinfo\n";
			}
		}
		// (whatever the LogStyle: this check used to sit in the
		// block above, so strict-inn allowed endless retries)
		if (clt.auth_failures >= clt.auth_max_failures) {
			(*clt.co) << "400 " PACKAGE " " VERSION
			    ", service discontinued (max passwords retries)\r\n";
			goto client_exit;
		}
	} while (errc == 0 || errc == -1);

      client_exit:
	if (clt.stat_artingrp) {
		slog.p(Logger::Notice) << clt.client_logname
		    << " group " << clt.groupname
		    << " " << clt.stat_artingrp << "\n";
		clt.stat_artingrp = 0;
	}
	slog.p(Logger::Notice) << clt.client_logname
	    << " exit articles " << clt.stat_articles
	    << " groups " << clt.stat_groups << "\n";
	clt.co->flush();
	delete clt.srvr;
}

/*
 * Creates a socket bound to addr, or returns -1. With dualstack (the IPv6
 * wildcard address of ListenTo DEFAULT), the socket accepts IPv4
 * connections too, as IPv4-mapped addresses.
 */
static int bind_listen_socket(const struct addrinfo *addr, bool dualstack,
			      int *bind_error)
{
	int sock = socket(addr->ai_family, SOCK_STREAM, 0);
	if (sock < 0) {
		*bind_error = errno;
		slog.p(dualstack ? Logger::Notice : Logger::Error)
		    << "socket failed: " << strerror(*bind_error) << "\n";
		return -1;
	}

	int one = 1;
	if (setsockopt(sock, SOL_SOCKET, SO_KEEPALIVE,
		       (char *) &one, sizeof(int)) < 0) {
		slog.p(Logger::Error) << "setsockopt failed: " <<
		    strerror(errno) << "\n";
	}

	if (setsockopt(sock, SOL_SOCKET, SO_REUSEADDR,
		       (char *) &one, sizeof(int)) < 0) {
		slog.p(Logger::Error) << "setsockopt failed: " <<
		    strerror(errno) << "\n";
	}

	if (dualstack) {
#ifdef IPV6_V6ONLY
		int zero = 0;
		if (setsockopt(sock, IPPROTO_IPV6, IPV6_V6ONLY,
			       (char *) &zero, sizeof(zero)) < 0) {
			*bind_error = errno;
			slog.p(Logger::Notice) << "IPv6 socket can't accept "
			    "IPv4 connections: " << strerror(*bind_error) << "\n";
			close(sock);
			return -1;
		}
#endif
	}

	if (bind(sock, addr->ai_addr, addr->ai_addrlen) < 0) {
		*bind_error = errno;
		slog.p(dualstack ? Logger::Notice : Logger::Error)
		    << "can't bind socket: " << strerror(*bind_error) << "\n";
		close(sock);
		return -1;
	}
	return sock;
}

// listen() backlog of the listening sockets
#define LISTEN_BACKLOG 128

/*
 * listen() failed: the daemon would run without that listener. On Linux
 * two sockets with SO_REUSEADDR can both bind the same address and port
 * (TLSListenTo the same as ListenTo, or another process's socket), and
 * only listen() then fails.
 */
static void listen_failed(const char *keyword, const char *spec)
{
	const int err = errno;
	slog.p(Logger::Error) << cmnd << ": can't listen on " << keyword << " "
	    << spec << ": " << strerror(err) << "\n";
	cerr << cmnd << ": can't listen on " << keyword << " " << spec << ": "
	    << strerror(err) << "\n";
	remove_pid_file();
	exit(1);
}

/*
 * Loads TLSCertFile, TLSKeyFile and TLSPriority for client connections.
 * At the start a failure is reported on stderr too (newscache exits then;
 * not in inetd mode, where stderr is the client's socket); on a reread the
 * certificate loaded before stays.
 */
static bool load_client_tls(bool at_start, bool to_stderr = false)
{
	string err;
	if (Cfg.TLSCertFile[0] == '\0')
		err = "TLSCertFile and TLSKeyFile are not set";
	else if (sockstream::load_server_tls(Cfg.TLSCertFile, Cfg.TLSKeyFile,
					       Cfg.TLSPriority, err)) {
		slog.p(Logger::Info) << "TLS: loaded " << Cfg.TLSCertFile << "\n";
		return true;
	}
	slog.p(Logger::Error) << "TLS: " << err
	    << (at_start ? "" : "; the certificate loaded before stays") << "\n";
	if (to_stderr)
		cerr << cmnd << ": " << err << "\n";
	return false;
}

/*
 * AuthRequiresTLS where it can't work: without Authentication it does
 * nothing, without TLS for clients nobody can log in.
 */
static void warn_auth_requires_tls(const AccessList &access_list,
				   bool tls_available)
{
	const vector<AccessEntry> &entries = access_list.entries();
	for (vector<AccessEntry>::const_iterator i = entries.begin();
	     i != entries.end(); ++i) {
		if (!i->auth_requires_tls)
			continue;
		const string p = i->pattern().empty() ? "Default" : i->pattern();
		if (i->authentication.getType() == "none")
			slog.p(Logger::Warning) << "AccessList " << p
			    << ": AuthRequiresTLS without Authentication does "
			    "nothing\n";
		else if (!tls_available)
			slog.p(Logger::Warning) << "AccessList " << p
			    << ": AuthRequiresTLS, but no TLS for clients "
			    "(TLSListenTo, or -t with -i): they can't log in\n";
	}
}

/*
 * The listening socket for spec (ListenTo, TLSListenTo: [address]:port,
 * address:port, address, DEFAULT; port defaults to nntp, 563 for TLS),
 * bound, not yet listening. plain: the ListenTo socket, for which CachePort
 * still works. Exits if it can't be had. The socket is non-blocking: with
 * two of them, an accept() must not wait on one (a connection that went
 * away after poll) while clients queue on the other.
 */
static int open_listener(const char *spec, const char *keyword, bool plain)
{
	const char *defservice = plain ? "nntp" : "563";
	/* create socket and set some socket options */
	string hostname;
	const char *nodename = NULL;
	const char *servname = NULL;
	struct addrinfo hints;

	memset(&hints, 0, sizeof(hints));
	// No AI_ADDRCONFIG: it ignores loopback addresses, so on a host
	// without a global IPv6 address (a container) even ListenTo [::1]
	// failed to resolve. The address is numeric; it is bound as given.
	hints.ai_flags = AI_PASSIVE | AI_NUMERICHOST;
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = SOCK_STREAM;
	hints.ai_protocol = IPPROTO_TCP;

	/* fill the sockaddr with the port we should listen too */
	if (plain && Cfg.CachePort[0] != '\0' && Cfg.ListenToSet) {
		slog.p(Logger::Notice) << "CachePort " << Cfg.CachePort
		    << " ignored: ListenTo is set\n";
	}
	if (plain && Cfg.CachePort[0] != '\0' && !Cfg.ListenToSet) {
		// the old way: all addresses (as ListenTo DEFAULT), on a
		// service name or a #number. (The test was the wrong way
		// round: "nntp" became the number "ntp", "#119" stayed
		// "#119", so CachePort never worked.)
		if (Cfg.CachePort[0] == '#') {
			servname = Cfg.CachePort + 1;
			hints.ai_flags |= AI_NUMERICSERV;
		} else {
			servname = Cfg.CachePort;
		}
	} else {
		if (spec[0] == '[') {
			const char *end = strrchr(spec, ']');
			if ((end == NULL) ||
			    ((end[1] != '\0') && (end[1] != ':'))) {
				slog.
				  p(Logger::
				    Error) << cmnd <<
				  ": Invalid parameter " << keyword << ": " <<
				  spec << "\n";
				cerr << cmnd << ": Invalid parameter " << keyword
				    << ": " << spec << "\n";
				exit(1);
			}

			hostname.assign(const_cast<const char *>(spec + 1), end);
			nodename = hostname.c_str();

			if (end[1] == ':') {
				servname = end + 2;
			} else {
				servname = defservice;
			}
		} else {
			const char *colon = strrchr(spec, ':');
			if (colon != NULL) {
				hostname.assign(const_cast<const char *>(spec), colon);
				servname = colon + 1;
			} else {
				hostname.assign(spec);
				servname = defservice;
			}

			if (hostname != "DEFAULT") {
				nodename = hostname.c_str();
			}
		}
	}

	struct addrinfo *res;
	if (getaddrinfo(nodename, servname, &hints, &res)) {
		slog.
		  p(Logger::
		    Error) << cmnd <<
		  ": Can't resolve parameter " << keyword << ": " <<
		  spec << "\n";
		cerr << cmnd << ": Can't resolve parameter " << keyword << ": "
		    << spec << "\n";
		exit(1);
	}

	/* For the wildcard address, one IPv6 socket takes IPv4 and IPv6
	 * clients; without IPv6, it falls back to the IPv4 address. Other
	 * addresses are tried in the order getaddrinfo returns them. */
	int sock = -1;
	// reported if no address was tried at all (not "Success")
	int bind_error = EADDRNOTAVAIL;
	struct addrinfo *addr;
	if (nodename == NULL) {
		for (addr = res; (sock < 0) && (addr != NULL); addr = addr->ai_next) {
			if (addr->ai_family == AF_INET6)
				sock = bind_listen_socket(addr, true, &bind_error);
		}
	}
	for (addr = res; (sock < 0) && (addr != NULL); addr = addr->ai_next) {
		if ((nodename == NULL) && (addr->ai_family == AF_INET6))
			continue;
		sock = bind_listen_socket(addr, false, &bind_error);
	}
	freeaddrinfo(res);
	if (sock < 0) {
		slog.p(Logger::Error) << cmnd << ": can't listen on "
		    << keyword << " " << spec << ": " << strerror(bind_error) << "\n";
		cerr << cmnd << ": can't listen on " << keyword << " " << spec
		    << ": " << strerror(bind_error) << "\n";
		exit(1);
	}

	int fl = fcntl(sock, F_GETFL);
	if (fl >= 0)
		fcntl(sock, F_SETFL, fl | O_NONBLOCK);
	return sock;
}

// Compare with what the daemon started with, not the preceding config read:
// a restart is still needed if the same changed value is reloaded again.
static void restart_notice(const char *name, const string &running,
			   const char *configured)
{
	if (running != configured)
		slog.p(Logger::Notice) << name << " changed (running: "
		    << running << ", config: " << configured
		    << "): restart newscache for that\n";
}

static void restart_notice(const char *name, int running, int configured)
{
	if (running != configured)
		slog.p(Logger::Notice) << name << " changed (running: "
		    << running << ", config: " << configured
		    << "): restart newscache for that\n";
}

void nntpd()
{
	// Keep only startup-only settings: retaining a full Config here would
	// unnecessarily retain upstream credentials after a config reload.
	const string startUsername(Cfg.Username), startGroupname(Cfg.Groupname);
	const string startPidFile(Cfg.PidFile), startHostname(Cfg.Hostname);
	const string startListenTo(Cfg.ListenTo), startTLSListenTo(Cfg.TLSListenTo);
	const string startCachePort(Cfg.CachePort);
#ifndef WITH_SYSLOG
	const string startLogDirectory(Cfg.LogDirectory);
#endif
	const int startServerType = Cfg.ServerType;
	const int startListenToSet = Cfg.ListenToSet;
	const int startNiceServer = Cfg.NiceServer;

	slog.p(Logger::Notice) << "NewsCache Server Start\n";
	warn_unsafe_auth_files(Cfg.clnts);
	warn_auth_requires_tls(Cfg.clnts, Cfg.TLSListenTo[0] != '\0');

	/* set nice value for master server */
#ifdef HAVE_SETPRIORITY
	errno = 0;
	int nice = Cfg.NiceServer;
	nice += getpriority(PRIO_PROCESS, 0);
	if (nice == -1 && errno != 0) {
		slog.
		    p(Logger::
		      Error) << "getpriority failed: " << strerror(errno)
		    << "\n";
	} else {
		if (setpriority(PRIO_PROCESS, 0, nice) == -1)
			slog.
			    p(Logger::
			      Error) << "setpriority failed: " <<
			    strerror(errno) << "\n";
	}
#endif

	/* nobody is connecting to NewsCache currently */
	nntp_connections = 0;

	int wake_pipe[2];
	if (pipe(wake_pipe) < 0) {
		slog.p(Logger::Error) << "cannot create signal wake pipe: "
		    << strerror(errno) << "\n";
		exit(1);
	}
	for (int i = 0; i < 2; i++) {
		int fl = fcntl(wake_pipe[i], F_GETFL);
		int fdfl = fcntl(wake_pipe[i], F_GETFD);
		if (fl < 0 || fdfl < 0 ||
		    fcntl(wake_pipe[i], F_SETFL, fl | O_NONBLOCK) < 0 ||
		    fcntl(wake_pipe[i], F_SETFD, fdfl | FD_CLOEXEC) < 0) {
			slog.p(Logger::Error) << "cannot set signal wake pipe flags: "
			    << strerror(errno) << "\n";
			close(wake_pipe[0]);
			close(wake_pipe[1]);
			exit(1);
		}
	}
	signal_wake_write = wake_pipe[1];

	int sock = open_listener(Cfg.ListenTo, "ListenTo", true);
	int tls_sock = -1;
	if (Cfg.TLSListenTo[0] != '\0')
		tls_sock = open_listener(Cfg.TLSListenTo, "TLSListenTo", false);

	write_pid_file();

	setugid(Cfg.Username, Cfg.Groupname);
	// A queue of 4 connections not accepted yet: a burst of clients (or
	// a busy moment) overflowed it, and FreeBSD then resets connections.
	// MaxConnections is the limit for sessions; this only holds
	// connections until the next accept().
	if (listen(sock, LISTEN_BACKLOG) < 0)
		listen_failed("ListenTo", Cfg.ListenTo);
	if (tls_sock >= 0 && listen(tls_sock, LISTEN_BACKLOG) < 0)
		listen_failed("TLSListenTo", Cfg.TLSListenTo);

	struct pollfd lfd[3];
	int nlfd = 0;
	lfd[nlfd].fd = sock;
	lfd[nlfd++].events = POLLIN;
	if (tls_sock >= 0) {
		lfd[nlfd].fd = tls_sock;
		lfd[nlfd++].events = POLLIN;
	}
	lfd[nlfd].fd = wake_pipe[0];
	lfd[nlfd].events = POLLIN;
	int next_lfd = 0;	// the listener asked first, in turns

	for (;;) {
		int clt_fd;
		int clt_pid = 0;
		bool clt_tls = false;

		/* Accept connection */
		do {
			if (reReadConfig) {
				reReadConfig = false;
				// parse into a copy, so a broken file keeps
				// the running configuration
				try {
					Config newCfg;
					newCfg.read(config_file);
					restart_notice("Username", startUsername, newCfg.Username);
					restart_notice("Groupname", startGroupname, newCfg.Groupname);
					restart_notice("PidFile", startPidFile, newCfg.PidFile);
#ifndef WITH_SYSLOG
					restart_notice("LogDirectory", startLogDirectory, newCfg.LogDirectory);
#endif
					restart_notice("ServerType", startServerType == Config::inetd ? "inetd" : "standalone",
					    newCfg.ServerType == Config::inetd ? "inetd" : "standalone");
					// without a ListenTo line, CachePort decides the port:
					// "ListenTo DEFAULT" given explicitly is a change too
					restart_notice("ListenTo",
					    startListenToSet ? startListenTo : string("(not set)"),
					    newCfg.ListenToSet ? newCfg.ListenTo : "(not set)");
					restart_notice("TLSListenTo", startTLSListenTo, newCfg.TLSListenTo);
					restart_notice("CachePort", startCachePort, newCfg.CachePort);
					restart_notice("Hostname", startHostname, newCfg.Hostname);
					restart_notice("NiceServer", startNiceServer, newCfg.NiceServer);
					// Sessions inherit the master's startup nice adjustment and
					// must not mix it with a new, unapplied master setting.
					newCfg.NiceServer = startNiceServer;
					Cfg = newCfg;
					warn_unsafe_auth_files(Cfg.clnts);
					warn_auth_requires_tls(Cfg.clnts,
							       tls_sock >= 0);
					// a new certificate (e.g. renewed): the
					// news user must be able to read the key
					if (tls_sock >= 0)
						load_client_tls(false);
					slog.p(Logger::Notice)
					    << "reread " << config_file << "\n";
				}
				catch(const Error & e) {
					slog.p(Logger::Error)
					    << "cannot reread " << config_file
					    << ", keeping the old configuration: "
					    << e._errtext << "\n";
				}
			}

			log_ended_sessions();
			errno = 0;
			clt_fd = -1;
			for (int i = 0; i <= nlfd; i++)
				lfd[i].revents = 0;
			// A signal before the pipe was installed is recorded in the
			// flags. Later signals leave a byte in the pipe even if they
			// arrive between this check and poll().
			if (Xsignal != -1 || reReadConfig)
				errno = EINTR;
			else if (poll(lfd, nlfd + 1, -1) > 0) {
				if (lfd[nlfd].revents & POLLIN) {
					char buf[128];
					while (read(wake_pipe[0], buf, sizeof(buf)) > 0)
						;
					errno = EINTR;
				} else {
					for (int k = 0; k < nlfd; k++) {
						const int i = (next_lfd + k) % nlfd;
						if (!(lfd[i].revents & POLLIN))
							continue;
						next_lfd = (i + 1) % nlfd;
						errno = 0;
						clt_fd = accept(lfd[i].fd, NULL, NULL);
						clt_tls = (lfd[i].fd == tls_sock);
						break;
					}
				}
				// the connection went away before accept(),
				// or no listener was ready after all: wait
				// again
				if (clt_fd < 0 && (errno == 0 || errno == EAGAIN ||
						   errno == EWOULDBLOCK ||
						   errno == ECONNABORTED))
					errno = EINTR;
			}
			if (Xsignal != -1) {
				close(sock);
				if (tls_sock >= 0)
					close(tls_sock);
				end_sessions();
				remove_pid_file();
				slog.p(Logger::Notice) << "NewsCache Server Stop "
				    "(signal " << (int) Xsignal << ")\n";
				exit(0);
			}
		} while (errno == EINTR);
		if (clt_fd < 0) {
			slog.
			    p(Logger::
			      Warning) << "accept failed: " <<
			    strerror(errno) << "\n";
			continue;
		}
		// blocking, as before: on BSD and macOS the accepted socket
		// inherits O_NONBLOCK from the listener
		{
			int fl = fcntl(clt_fd, F_GETFL);
			if (fl >= 0 && (fl & O_NONBLOCK))
				fcntl(clt_fd, F_SETFL, fl & ~O_NONBLOCK);
		}
#ifdef CONF_MultiClient
		// SIGCHLD blocked until the new session is in the table: a
		// session that ended at once would stay there for good
		sigset_t old_mask;
		block_fork_signals(&old_mask);
		if (nntp_connections >= Cfg.MaxConnections
		    || (clt_pid = fork()) < 0) {
			sigprocmask(SIG_SETMASK, &old_mask, NULL);
			if (clt_pid < 0) {
				slog.
				    p(Logger::
				      Error) << "fork failed: " <<
				    strerror(errno) << "\n";
			}
			// not on the TLS listener: there it would be
			// plain text where the client expects a TLS record
			static const char too_many[] = "400 too many users\r\n";
			if (!clt_tls &&
			    write(clt_fd, too_many, sizeof(too_many) - 1) < 0) {
				// refused anyway; nothing else to do
			}
			close(clt_fd);
		} else {
			// success
			if (clt_pid == 0) {
				// child
				signal_wake_write = -1;
				close(wake_pipe[0]);
				close(wake_pipe[1]);
				Xsignal = -1;
				alarmed = 0;
				sigprocmask(SIG_SETMASK, &old_mask, NULL);
				sessions.clear();
				close(sock);
				if (tls_sock >= 0)
					close(tls_sock);
				if (pid_file_fd >= 0) {
					close(pid_file_fd);
					pid_file_fd = -1;
				}

				try {
					nnrpd(clt_fd, clt_tls);
				} catch (const UsageError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "UsageError ";
					e.print();
				} catch (const NotAllowedError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "NotAllowdError ";
					e.print();
				} catch (const NoSuchArticleError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "NoSuchArticleError ";
					e.print();
				} catch (const DuplicateArticleError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "DuplicateArticleError ";
					e.print();
				} catch (const NoSuchGroupError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "NoSuchGroupError ";
					e.print();
				} catch (const NoNewsServerError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "NoNewsServerError ";
					e.print();
				} catch (const NoSuchFieldError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "NoSuchFieldError ";
					e.print();
				} catch (const NSError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "NSError ";
					e.print();
				} catch (const AssertionError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "AssertionError ";
					e.print();
				} catch (const IOError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "IOError ";
					e.print();
				} catch (const SystemError &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "SystemError ";
					e.print();
				} catch (const Error &e) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "Error ";
					e.print();
				} catch ( ... ) {
					slog.p(Logger::Error) << "nnrpd caught "
						<< "unknown!!"	<< "\n";
				}
				close(clt_fd);
				exit(0);
			}
			//Parent
			add_session(clt_pid);
			nntp_connections = nntp_connections + 1;
			sigprocmask(SIG_SETMASK, &old_mask, NULL);

			while ((close(clt_fd) < 0) && (errno == EINTR)) {
			}
		}
#else
		nnrpd(clt_fd, clt_tls);
		close(clt_fd);
#endif
	}

	close(sock);
	if (tls_sock >= 0)
		close(tls_sock);
}

void sigchld(int num)
{
	int pid;
	int st;
	int saved_errno = errno;	// waitpid may change it

	/* Reinstall the signal handler */
#ifdef HAVE_SIGACTION
	struct sigaction action;
	action.sa_handler = sigchld;
	sigemptyset(&action.sa_mask);
	action.sa_flags = 0;
	sigaction(num, &action, NULL);
#else
	signal(num, sigchld);
#endif

	while ((pid = waitpid(0, &st, WNOHANG)) > 0) {
		if (WIFEXITED(st) || WIFSIGNALED(st)) {
			// child terminated
			nntp_connections = nntp_connections - 1;
			for (size_t i = 0; i < sessions.size(); i++)
				if (sessions[i] == pid)
					sessions[i] = 0;
			// no logging here (see log_ended_sessions)
			if (st) {
				if (ended_n < ENDED_MAX) {
					ended_pid[ended_n] = pid;
					ended_status[ended_n] = st;
					ended_n = ended_n + 1;
				} else
					ended_lost = ended_lost + 1;
			}
		}
	}
	wake_master();
	errno = saved_errno;
}

void catchsigalarm(int num)
{
	alarmed=1;
#ifdef HAVE_SIGACTION
	/* Reinstall the signal handler */
	struct sigaction action;
	action.sa_handler = catchsigalarm;
	sigemptyset(&action.sa_mask);
	action.sa_flags = 0;
	sigaction(num, &action, NULL);
#else
	signal(num, catchsigalarm);
#endif
}

void catchsighup(int num)
{
	reReadConfig=true;
	wake_master();
#ifdef HAVE_SIGACTION
	/* Reinstall the signal handler */
	struct sigaction action;
	action.sa_handler = catchsighup;
	sigemptyset(&action.sa_mask);
	action.sa_flags = 0;
	sigaction(num, &action, NULL);
#else
	signal(num, catchsighup);
#endif
}

void catchsignal(int num)
{
	Xsignal = num;
	// A session waiting for the client (a command, or a send it doesn't
	// read) stops at once: the EINTR loops of sockstream end on alarmed
	// only, so SIGTERM was noticed after the next command line.
	alarmed = 1;
	wake_master();
#ifdef HAVE_SIGACTION
	/* Reinstall the signal handler */
	struct sigaction action;
	action.sa_handler = catchsignal;
	sigemptyset(&action.sa_mask);
	action.sa_flags = 0;
	sigaction(num, &action, NULL);
#else
	signal(num, catchsignal);
#endif
}

#define USAGE \
"       -h, --help\n"\
"              Show summary of options.\n"\
"\n"\
"       -v, --version\n"\
"              Show version of program.\n"\
"\n"\
"       -f --fqdn\n"\
"              Print what newscache thinks is the fully qualified domain  name.\n"\
"\n"\
"       -c --configuration config-file\n"\
"\n"\
"       -i --inetd\n"\
"              newscache is used with inetd and read from stdin.\n"\
"\n"\
"       -d --debug\n"\
"              Do not detach.\n"\
"\n"\
"       -p --print-parameter\n"\
"              Print current parameter settings.\n"\
"\n"\
"       -n --no-fork\n"\
"              Do not fork.\n"\
"\n"\
"       -t --tls\n"\
"              As -i, but the client's connection (standard input, a socket)\n"\
"              is TLS (TLSCertFile, TLSKeyFile).\n"\
"\n"\
"       -o --configuration-options\n"\
"              Print all options specified in the configure phase.\n"

int main(int argc, char **argv)
{
#ifndef WITH_SYSLOG
	char logfile[MAXPATHLEN];
#endif
	int opt_inetd = 0, opt_debug = 0, opt_print_para = 0;
	int opt_nofork = 0, opt_config_options = 0, opt_tls = 0;
	int aerr = 0, c;

	snprintf(config_file, sizeof(config_file), "%s/newscache.conf", SYSCONFDIR);

	cmnd = argv[0];
	while (1) {
#ifdef HAVE_GETOPT_H
		int option_index = 0;
		static struct option long_options[] = {
			{"version", 0, 0, 'v'},
			{"fqdn", 0, 0, 'f'},
			{"help", 0, 0, 'h'},
			{"configuration", 1, 0, 'c'},
			{"inetd", 0, 0, 'i'},
			{"debug", 0, 0, 'd'},
			{"no-fork", 0, 0, 'n'},
			{"tls", 0, 0, 't'},
			{"print-parameter", 0, 0, 'p'},
			{"configuration-options", 0, 0, 'o'},
			{0, 0, 0, 0}
		};

		c = getopt_long (argc, argv, "vfhc:idnpot", long_options,
				&option_index);
#else
		c = getopt (argc, argv, "vfhc:idnpot");
#endif
		if (c == -1)
			break;

		switch (c) {
			case 'v':
				cout << PACKAGE << " " VERSION << endl;
				exit (0);
				break;

			case 'f':
				cout << getfqdn () << endl;
				exit (0);
				break;

			case 'h':
				cout << "Usage: " << cmnd << " [options]\n"
					<< USAGE;
				exit (0);
				break;

			case 'c':
				if (snprintf(config_file, sizeof(config_file), "%s", optarg) >=
				    (int) sizeof(config_file)) {
					cerr << cmnd << ": configuration file name is too long (at most "
					     << sizeof(config_file) - 1 << " characters)\n";
					exit(1);
				}
				break;

			case 'i':
				++opt_inetd;
				break;

			case 'd':
				++opt_debug;
				break;

			case 'p':
				++opt_print_para;
				break;

			case 'o':
				++opt_config_options;
				break;

			case 'n':
				++opt_nofork;
				break;

			case 't':
				// inetd mode with TLS on standard input
				++opt_tls;
				++opt_inetd;
				break;

			default:
				aerr = 1;
				break;
		}
	}

	if (aerr || optind != argc) {
		cerr << "Usage: " << cmnd << " [options]\n" << USAGE;
		exit(1);
	}

	// -o prints how the binary was built: no configuration needed, so
	// that it works on a broken or missing one too (like -v)
	if (opt_config_options) {
		for (int i=0; ConfigurationOptions[i] != NULL; i++) {
			cout << ConfigurationOptions[i] << " ";
		}
		cout << endl;
		exit (0);
	}

	try {
		Cfg.read(config_file);
		snprintf(nntp_hostname, sizeof(nntp_hostname), "%s", Cfg.Hostname);
	}
	catch(const IOError & io) {
		if (io._errno == -1)
			cerr << "unexpected EOF in " << config_file << "\n";
		else
			cerr << io._errtext << ": " << strerror(io._errno) << "\n";
		exit(2);
	}
	catch(const SyntaxError & se) {
		cerr << se._errtext << "\n";
		exit(2);
	}

	// -p checks the TLS certificate, key and TLSPriority too (it needs
	// to be able to read the key)
	if (opt_print_para && Cfg.TLSCertFile[0] != '\0' &&
	    !load_client_tls(true, true))
		exit(2);

	if (opt_print_para) {
		cout << "# This output is generated with newscache -p" << endl;
		Cfg.printParameters (&cout);
		exit (0);
	}

#ifdef WITH_SYSLOG
	slog.open(PACKAGE, LOG_NDELAY | LOG_PID, LOG_NEWS);
#else
	if (snprintf(logfile, sizeof(logfile), "%s/newscache.log",
		     Cfg.LogDirectory) >= (int) sizeof(logfile)) {
		cerr << "LogDirectory is too long\n";
		exit(1);
	}
	slog.open (logfile);
#endif

	// signal handling
	sockstream::alarm_indicator(alarmed);
	Xsignal = -1;
	reReadConfig = false;

#ifdef HAVE_SIGACTION
	struct sigaction action;

	// alarm - client timeout signal
	action.sa_handler = catchsigalarm;
	sigemptyset(&action.sa_mask);
	action.sa_flags = 0;
	sigaction(SIGALRM, &action, NULL);

	// reread config file
	action.sa_handler = catchsighup;
	sigemptyset(&action.sa_mask);
	action.sa_flags = 0;
	sigaction(SIGHUP, &action, NULL);

	// signals indicating to terminate NewsCache
	action.sa_handler = catchsignal;
	sigaction(SIGINT, &action, NULL);
	sigaction(SIGTERM, &action, NULL);

	// ignored signals; SIGPIPE too: a write to a connection the other
	// side has closed fails with EPIPE, which the streams handle (an
	// upstream connection is opened again). As a termination signal it
	// ended the client's session ("service discontinued") whenever an
	// upstream connection had been reset. SIGXFSZ: a write beyond the
	// file size limit (ulimit -f) fails with EFBIG then, which the spool
	// handles as a full disk, instead of ending the session.
	action.sa_handler = SIG_IGN;
	sigaction(SIGPIPE, &action, NULL);
	sigaction(SIGXFSZ, &action, NULL);
	sigaction(SIGUSR1, &action, NULL);
	sigaction(SIGUSR2, &action, NULL);

	// clean zombies
	action.sa_handler = sigchld;
	sigaction(SIGCHLD, &action, NULL);
#else
	signal(SIGALRM, catchsigalarm);
	signal(SIGHUP, catchsighup);
	signal(SIGINT, catchsignal);
	signal(SIGPIPE, SIG_IGN);
	signal(SIGXFSZ, SIG_IGN);
	signal(SIGTERM, catchsignal);

	signal(SIGUSR1, SIG_IGN);
	signal(SIGUSR2, SIG_IGN);

	signal(SIGCHLD, sigchld);
#endif

	if (Cfg.ServerType == Config::inetd)
		opt_inetd = 1;

	// The certificate and key for TLS clients: loaded now, as the user
	// that starts newscache (often root, who may read the key), before
	// setugid and before the daemon detaches, so that an error reaches
	// stderr. The sessions inherit them.
	if ((opt_inetd && opt_tls) || (!opt_inetd && Cfg.TLSListenTo[0] != '\0')) {
		if (!load_client_tls(true, !opt_inetd))
			exit(1);
	}

	if (opt_inetd) {
		warn_unsafe_auth_files(Cfg.clnts);
		warn_auth_requires_tls(Cfg.clnts, opt_tls != 0);
		if (getuid() == CONF_UIDROOT)
			setugid(Cfg.Username, Cfg.Groupname);
		// with -t, standard input is the client's socket; the plain
		// inetd mode reads cin and writes cout, as before
		if (opt_tls)
			nnrpd(STDIN_FILENO, true);
		else
			nnrpd(-1, false);
	} else {
		if (opt_debug == 0 && opt_nofork == 0) {
			switch (fork()) {
			case -1:
				cerr << "cannot fork\n";
				slog.p(Logger::Error) << "cannot fork\n";
				break;
			case 0:
				if (setsid() == -1) {
					cerr << "setsid failed\n";
					slog.
					    p(Logger::
					      Error) << "setsid failed\n";
					exit(1);
				}
				// /dev/null, not closed: else the next files
				// opened became fds 0-2, and what went to stderr
				// (an abort message, setugid's errors) went to a
				// client's socket or into a spool file
				{
					int fd = open("/dev/null", O_RDWR);
					if (fd >= 0) {
						dup2(fd, STDIN_FILENO);
						dup2(fd, STDOUT_FILENO);
						dup2(fd, STDERR_FILENO);
						if (fd > STDERR_FILENO)
							close(fd);
					}
				}
				break;
			default:
				return 0;
			}
		}
		nntpd();
	}

	return 0;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
