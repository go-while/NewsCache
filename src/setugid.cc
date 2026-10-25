#include <errno.h>
#include <sys/types.h>
#include <grp.h>
#include <pwd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include <iostream>
#include <string>

#include "setugid.h"
#include "Logger.h"

using namespace std;

extern Logger slog;

// Reports on stderr and in the log, then exits: the detached daemon has
// no stderr, and it died without a trace when the Username didn't exist.
static void fail(const string & msg)
{
	cerr << msg << endl;
	slog.p(Logger::Error) << msg << "\n";
	exit(1);
}

// Switches to the user uname and the group gname (either may be NULL or
// empty). Started as root, the group defaults to the user's primary group,
// and the supplementary groups are replaced first: by uname's groups
// (initgroups), or by just the new group if there is no user. Before, the
// process kept root's groups, group 0 among them, and with only a Username
// also root's primary group.
void setugid(const char *uname, const char *gname)
{
	bool have_gid = false, have_uid = false;
	gid_t gid = 0;
	uid_t uid = 0;
	string user;

	// Copy what is needed: initgroups() may reuse the static buffers of
	// getgrnam() and getpwnam().
	if (gname && *gname) {
		struct group *grp = getgrnam(gname);
		if (grp == NULL)
			fail(string("Cannot get grent for ") + gname +
			     ": please set the Groupname in newscache.conf");
		gid = grp->gr_gid;
		have_gid = true;
	}
	if (uname && *uname) {
		struct passwd *pwd = getpwnam(uname);
		if (pwd == NULL)
			fail(string("Cannot get pwent for ") + uname +
			     ": please set the Username in newscache.conf");
		uid = pwd->pw_uid;
		user = pwd->pw_name;
		if (!have_gid && geteuid() == 0) {
			gid = pwd->pw_gid;
			have_gid = true;
		}
		have_uid = true;
	}

	if (have_gid && geteuid() == 0) {
		int r = have_uid ? initgroups(user.c_str(), gid)
		    : setgroups(1, &gid);
		if (r < 0)
			fail(string("Cannot set the supplementary groups: ") +
			     strerror(errno));
	}
	char id[32];
	if (have_gid && setregid(gid, gid) < 0) {
		string err = strerror(errno);
		snprintf(id, sizeof(id), "%ld", (long) gid);
		fail(string("Cannot set effective groupid to ") + id + ": " +
		     err);
	}
	if (have_uid && setreuid(uid, uid) < 0) {
		string err = strerror(errno);
		snprintf(id, sizeof(id), "%ld", (long) uid);
		fail(string("Cannot set effective userid to ") + id + ": " +
		     err);
	}
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
