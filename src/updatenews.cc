/* updatenews.cc (c) 1996 by Thomas GSCHWIND 
 * -- prefetch a set of newsgroups
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
#include "config.h"

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
#include <sys/types.h>
#include <sys/socket.h>
#include <sys/wait.h>

#include <algorithm>
#include <iostream>
#include <set>
#include <vector>

#include "Debug.h"
#include "NewsCacheConfig.h"
#include "NServer.h"
#include "History.h"
#include "Logger.h"
#include "setugid.h"
#include "wildmat.h"

using namespace std;

#ifndef WITH_UNIQUE_PACKAGE_NAME
#undef PACKAGE
#define PACKAGE PACKAGE_UPDATENEWS
#endif

//Notes {
// Config überarbeiten. Bei Syntaxfehler beenden. 
// Switch ?-d? der nur Config einliest und ausgibt.
//} Notes

enum {
	F_PREFETCH_ACTIVE = 0x01,
	F_PREFETCH_GROUP = 0x02,
	F_PREFETCH_OVER = 0x04,
	F_PREFETCH_LOCKGROUP = 0x08
};

Logger slog;
const char *cmnd;
Config Cfg;
int opt_flags = 0;
int opt_workers = 0;
int Xsignal;

void catchsignal(int num)
{
	Xsignal = num;
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

void doprefetch(CServer * cs, const char *group)
{
	// A corrupt database is moved aside and a new one started (see
	// NVcontainer::corrupt): then the prefetch is tried again, once.
	for (int attempt = 0; attempt < 2; attempt++) {
		Newsgroup *grp = NULL;
		try {
			slog.p(Logger::Info) << "prefetching " << group << "\n";
			grp = cs->getgroup(group);
			if (opt_flags & F_PREFETCH_GROUP)
				grp->
				    prefetchGroup(opt_flags &
						  F_PREFETCH_LOCKGROUP);
			else if (opt_flags & F_PREFETCH_OVER)
				grp->prefetchOverview();
			cs->freegroup(grp);
			return;
		}
		catch(const CorruptDBError & e) {
			if (grp)
				cs->freegroup(grp);
			continue;
		}
		catch(...) {
			break;
		}
	}
	slog.p(Logger::Error) << "prefetching " << group << " failed\n";
}

int create_worker(CServer * cs, const char *group)
{
	int c;
	switch (c = fork()) {
	case -1:
		// failed
		slog.
		    p(Logger::
		      Error) << "fork failed, using this process\n";
		doprefetch(cs, group);
		return 0;
	case 0:
		// child
		doprefetch(cs, group);
		exit(0);
	default:
		// parent
		return 1;
		break;
	}
}

// Is this prefetch-file line a wildmat pattern rather than a group name?
static bool is_pattern(const string & line)
{
	return line.find_first_of("*?[\\") != string::npos;
}

/*
 * Reads the prefetch file. Each line is one of
 *   group.name     prefetch this group;
 *   pattern        a wildmat pattern (*, ?, [...]): prefetch every group
 *                  of the active database that matches it;
 *   !pattern       don't prefetch the groups that match it (a group name
 *                  works too), wherever they come from in the file.
 * Empty lines and lines starting with # are skipped. Each group is
 * prefetched once, in the order of the file; the groups of one pattern
 * in alphabetical order.
 */
void readgroups(const char *fn, ActiveDB * active, vector < string > &groups)
{
	ifstream ifs(fn);
	string line;
	vector < string > want, skip;

	if (!ifs.good()) {
		return;
	}

	for (; Xsignal < 0;) {
		nlreadline(ifs, line, 0);
		if (ifs.eof())
			break;
		if (ifs.bad()) {
			slog.
			    p(Logger::Error) << "cannot read from " << Cfg.
			    PrefetchFile << "\n";
			break;
		}
		string::size_type b = line.find_first_not_of(" \t\r\n");
		if (b == string::npos || line[b] == '#')
			continue;
		line = line.substr(b, line.find_last_not_of(" \t\r\n") + 1 - b);
		if (line[0] == '!') {
			if (line.size() > 1)
				skip.push_back(line.substr(1));
		} else {
			want.push_back(line);
		}
	}
	ifs.close();

	vector < string > all;	// the active database's groups, sorted
	bool have_all = false;
	set < string > seen;
	for (vector < string >::iterator w = want.begin(); w != want.end();
	     ++w) {
		vector < string > cand;
		if (!is_pattern(*w)) {
			cand.push_back(*w);
		} else {
			if (!have_all) {
				for (Iter < GroupInfo > i = active->begin(), e =
				     active->end(); i != e; ++i)
					all.push_back(i->name());
				sort(all.begin(), all.end());
				have_all = true;
			}
			for (vector < string >::iterator g = all.begin();
			     g != all.end(); ++g)
				if (wildmat(g->c_str(), w->c_str()))
					cand.push_back(*g);
			if (cand.empty())
				slog.p(Logger::Notice) << "prefetch pattern " << *w
				    << " matches no group\n";
		}
		for (vector < string >::iterator c = cand.begin();
		     c != cand.end(); ++c) {
			bool skipped = false;
			for (vector < string >::iterator k = skip.begin();
			     k != skip.end() && !skipped; ++k)
				skipped = wildmat(c->c_str(), k->c_str());
			if (!skipped && seen.insert(*c).second)
				groups.push_back(*c);
		}
	}
}

void update(void)
{
	CServer cs(Cfg.SpoolDirectory, &(Cfg.srvrs));
	int wi = opt_workers ? 0 : -1;

	cs.setttl(Cfg.ttl_list, Cfg.ttl_desc);
	cs.setmaxarticlesize(Cfg.MaxArticleSize);

	slog.p(Logger::Info) << "posting spooled articles\n";
	cs.postspooled();
	if (opt_flags & F_PREFETCH_ACTIVE) {
		slog.p(Logger::Debug) << "invalidate the ActiveDB (force update)\n";
		cs.invalidateActiveDB ();
	};
	slog.p(Logger::Info) << "prefetching active database\n";
	ActiveDB *active = cs.active();

	if (!(opt_flags & (F_PREFETCH_GROUP | F_PREFETCH_OVER)))
		return;

	// read groups to prefetch
	vector < string > groups;
	vector < string >::iterator begin, end;
	readgroups(Cfg.PrefetchFile, active, groups);

	for (begin = groups.begin(), end = groups.end();
	     begin != end && Xsignal < 0; ++begin) {
		while (wi == opt_workers) {
			// maximum workers spawned, wait until a worker exits
			int cstat;
			if (wait(&cstat) < 0) {
				if (errno == EINTR)
					continue;
				// no child left (ECHILD): nothing to wait for
				wi = 0;
				break;
			}
			if (WIFSIGNALED(cstat)) {
				slog.
				    p(Logger::
				      Error) <<
				    "worker terminated unexpectedly\n";
				--wi;
			} else if (WIFEXITED(cstat)) {
				--wi;
			}
		}
		if (opt_workers == 0)
			doprefetch(&cs, begin->c_str());
		else
			wi += create_worker(&cs, begin->c_str());
	}
}

#define USAGE \
"       -h, --help\n"\
"              Show summary of options.\n"\
"\n"\
"       -v, --version\n"\
"              Show version of program.\n"\
"\n"\
"       -c --configuration config-file\n"\
"\n"\
"       -a --active\n"\
"\n"\
"       -g --group\n"\
"              Prefetch articles.\n"\
"\n"\
"       -o --over\n"\
"              Prefetch overview database.\n"\
"\n"\
"       -l --lock-groups\n"\
"              Lock newsgroup, when prefetching.\n"\
"\n"\
"       -w --workers\n"\
"              Prefetch # newsgroups at once.\n"\

int main(int argc, char **argv)
{
#ifndef WITH_SYSLOG
	char logfile[MAXPATHLEN];
	time_t t;
	pid_t p;
#endif
	char conffile[MAXPATHLEN];
	int opt_flagsset = 0;
	int c, aerr = 0;

	snprintf(conffile, sizeof(conffile), "%s/newscache.conf", SYSCONFDIR);

	cmnd = argv[0];
	while (1) {
#ifdef HAVE_GETOPT_H
		int option_index = 0;
		static struct option long_options[] = {
			{"version", 0, 0, 'v'},
			{"help", 0, 0, 'h'},
			{"active", 0, 0, 'a'},
			{"group", 0, 0, 'g'},
			{"over", 0, 0, 'o'},
			{"lock-group", 0, 0, 'l'},
			{"workers", 1, 0, 'w'},
			{"configuration", 1, 0, 'c'},
			{0, 0, 0, 0}
		};

		c = getopt_long (argc, argv, "vhc:agolw:", long_options,
				&option_index);
#else
		c = getopt (argc, argv, "vhc:agolw:");
#endif
		if (c == -1)
			break;

		switch (c) {
			case 'v':
				cout << PACKAGE << " " << VERSION << endl;
				exit (0);
				break;

			case 'h':
				cout << "Usage: " << cmnd << endl;
				cout << USAGE << endl;
				exit (0);
				break;

			case 'a':
				opt_flags |= F_PREFETCH_ACTIVE;
				opt_flagsset = 1;
				break;

			case 'g':
				opt_flags |= F_PREFETCH_GROUP;
				opt_flagsset = 1;
				break;

			case 'o':
				opt_flags |= F_PREFETCH_OVER;
				opt_flagsset = 1;
				break;

			case 'l':
				opt_flags |= F_PREFETCH_LOCKGROUP;
				opt_flagsset = 1;
				break;

			case 'c':
				if (snprintf(conffile, sizeof(conffile), "%s", optarg) >=
				    (int) sizeof(conffile)) {
					cerr << cmnd << ": configuration file name is too long (at most "
					     << sizeof(conffile) - 1 << " characters)\n";
					exit(1);
				}
				break;

			default:
				aerr = 1;
				break;
		}

	}
	if (!opt_flagsset)
		opt_flags = F_PREFETCH_GROUP;

	if (aerr || optind != argc) {
		cerr << "Usage: " << cmnd << " [options]\n" << USAGE;
		exit(1);
	}

	try {
		Cfg.read(conffile);
		strcpy(nntp_hostname, Cfg.Hostname);
	}
	catch(IOError & io) {
		cerr << "unexpected EOF in " << conffile << "\n";
		exit(2);
	}
	catch(SyntaxError & se) {
		cerr << se._errtext << "\n";
		exit(2);
	}

	for (unsigned int i = 0; i < Cfg.srvrs.entries.size(); i++) {
		// Switch off offline mode
		Cfg.srvrs.entries[i].flags &=
		    ~(MPListEntry::F_OFFLINE | MPListEntry::F_SEMIOFFLINE);
		// Set timeout to 0
		Cfg.srvrs.entries[i].groupTimeout = 0;
	}

	// signal
	Xsignal = -1;
#ifdef HAVE_SIGACTION
	struct sigaction action;
	// signals indicating to terminate NewsCache
	action.sa_handler = catchsignal;
	sigemptyset(&action.sa_mask);
	action.sa_flags = 0;
	sigaction(SIGHUP, &action, NULL);
	sigaction(SIGINT, &action, NULL);
	sigaction(SIGPIPE, &action, NULL);
	sigaction(SIGTERM, &action, NULL);

	sigaction(SIGALRM, &action, NULL);
	sigaction(SIGUSR1, &action, NULL);
	sigaction(SIGUSR2, &action, NULL);
#else
	signal(SIGHUP, catchsignal);
	signal(SIGPIPE, catchsignal);
	signal(SIGINT, catchsignal);
	signal(SIGPIPE, catchsignal);
	signal(SIGTERM, catchsignal);
	signal(SIGALRM, catchsignal);
	signal(SIGUSR1, catchsignal);
	signal(SIGUSR2, catchsignal);
#endif

#ifdef WITH_SYSLOG
	slog.open(PACKAGE, LOG_NDELAY | LOG_PID, LOG_NEWS);
#else
	if (snprintf(logfile, sizeof(logfile), "%s/newscache_updatenews.log",
		     Cfg.LogDirectory) >= (int) sizeof(logfile)) {
		cerr << "LogDirectory is too long\n";
		exit(1);
	}
	slog.open (logfile);
#endif

	// Check the queue in each server-directory and send it off to the 
	// news server
	if (getuid() == CONF_UIDROOT)
		setugid(Cfg.Username, Cfg.Groupname);
	if (Cfg.HistoryEnabled)
		History::setspooldir(Cfg.SpoolDirectory);
	update();
	return 0;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
