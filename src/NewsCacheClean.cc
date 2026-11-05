/* NewsCacheClean.cc 
 * -- (c) 1997-1998 by Thomas GSCHWIND <tom@infosys.tuwien.ac.at>
 * -- removes the least recently used articles/groups until the 
 * -- size of the spool directory is below SpoolSize
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
 * Copyright (C) 2001-2003 Herbert Straub
 * - change from chain to table (performance)
 * - adding nice value for NewsCacheClean
 * - removing outdated .art files from resized .db files
 * - adding option -t (try_flag)
 * - adding option -p (print purge table) - debug option
 * 2003-03-11 Herbert Straub
 * - changing table implemention with STL vector
 * - removing all structures and function implementing the table 
 */
#include "config.h"

#include <errno.h>
#include <fcntl.h>
#include <stdlib.h>
#include <string.h>
#ifdef HAVE_GETOPT_H
#include <getopt.h>
#endif
#include <sys/file.h>
#include <sys/param.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/types.h>
#include <dirent.h>
#include <unistd.h>
#ifdef HAVE_SYS_TIME_H
#include <sys/time.h>
#endif
#ifdef HAVE_SETPRIORITY
#include <sys/resource.h>
#endif

#include <string>
#include <map>
#include <vector>
#include <algorithm>

#include "Debug.h"
#include "setugid.h"
#include "NewsCacheConfig.h"
#include "Logger.h"
#include "NServer.h"
#include "History.h"
#include "ObjLock.h"

using namespace std;

#ifndef WITH_UNIQUE_PACKAGE_NAME
#undef PACKAGE
#define PACKAGE PACKAGE_NEWSCACHECLEAN
#endif

#define MAX(a,b) (((a)>(b))?(a):(b))
#define FILE_ACCESS_TABLE_ENTRIES 100000

Logger slog;
const char *cmnd;
Config Cfg;

class Entry {
  public:
	Entry (const char *path, time_t time, long long blocks) 
		: Path(path), atime(time), blocks(blocks), known(false)
	{ }
	// The file as it was scanned: it is removed only if it is still that
	// file (a session may have stored a new one under the name since).
	Entry (const char *path, time_t time, const struct stat &st)
		: Path(path), atime(time), blocks(st.st_blocks), known(true),
		  dev(st.st_dev), ino(st.st_ino)
	{ }

	void print (ostream &out)
	{
		out << Path << " " << atime << " " << blocks << " " << endl;
	}
	
	string Path;
	time_t atime;
	long long blocks;
	bool known;
	dev_t dev;
	ino_t ino;
};


vector<Entry*> pvector;
bool sort_entries (const Entry *l, const Entry *r);
void clean(const char *cpath);

struct cache_statistic {
	size_t stPurgedDbCnt;	/* purged .db file count */
	size_t stPurgedDirCnt;	/* purged directory count */
	size_t stPurgedArtCnt;	/* purged .art* file count */
	size_t stPurgedOutArtCnt;
				/* purged outdated .art* file count */
	size_t stDbCnt;		/* overall .db file count before purge */
	size_t stDirCnt;	/* group directories with a database before purge */
	size_t stArtCnt;	/* overall .art file count before purge */
	size_t stOutArtCnt;	/* overall outdated .art file count before p.*/
	size_t stPurgedDbSize;	/* purged .db filesize */
	size_t stPurgedArtSize;	/* purged .art* filesize */
	size_t stPurgedOutArtSize;
				/* purged outdated .art* filesize */
	size_t stDbSize;	/* overall .db filesize before purge */
	size_t stArtSize;	/* overall .art* filesize before purge */
	size_t stOutArtSize;	/* overall outdated .art filesize before p. */
};

long long _blocks = 0;
long long _history_blocks = 0;	// of them, the history's shard files

static void print_statistic(ostream &out, struct cache_statistic *pS);
void remove_elements(long long max_blocks, struct cache_statistic *pS);

static void print_statistic(ostream &out, struct cache_statistic *pS)
{
	out << "PurgedDbCnt: " << pS->stPurgedDbCnt << endl;
	out << "PurgedDirCnt: " << pS->stPurgedDirCnt << endl;
	out << "PurgedArtCnt: " << pS->stPurgedArtCnt << endl;
	out << "PurgedOutArtCnt: " << pS->stPurgedOutArtCnt << endl;
	out << "DbCnt: " << pS->stDbCnt << endl;
	out << "DirCnt: " << pS->stDirCnt << endl;
	out << "ArtCnt: " << pS->stArtCnt << endl;
	out << "OutArtCnt: " << pS->stOutArtCnt << endl;
	out << "PurgedDbSize: " << pS->stPurgedDbSize / 2 /
	    1024 << "Mb" << endl;
	out << "PurgedArtSize: " << pS->stPurgedArtSize / 2 /
	    1024 << "Mb" << endl;
	out << "DbSize: " << pS->stDbSize / 2 / 1024 << "Mb" << endl;
	out << "ArtSize: " << pS->stArtSize / 2 / 1024 << "Mb" << endl;
	out << "OutArtSize: " << pS->stOutArtSize /2 / 1024 << "MB" << endl;
}

bool sort_entries (const Entry *l, const Entry *r)
{
	if (l->atime < r->atime)
		return 1;
	else
		return 0;
}


static void collect_statistics(struct cache_statistic *pS)
{
	vector<Entry *>::const_iterator begin, end;

	for (begin=pvector.begin(), end=pvector.end(); begin != end; begin++) {
		const string &path = (*begin)->Path;
		if (path.length() >= 3 &&
		    path.compare(path.length() - 3, 3, ".db") == 0) {
			pS->stDbCnt++;
			pS->stDirCnt++;
			pS->stDbSize += (*begin)->blocks;
		} else if ((*begin)->atime != 0) {
			pS->stArtCnt++;
			pS->stArtSize += (*begin)->blocks;
		} else {
			pS->stOutArtCnt++;
			pS->stOutArtSize += (*begin)->blocks;
		}
	}
}


#ifdef NEWSCACHE_TEST_CLEAN_RACE
// Compiled test fixture: waits once, after an .art file was found to be
// the one scanned and before it is removed, so that a session can try
// to store a new file under the name then (tests/e2e
// TestNewscachecleanUnlinkReplaced).
static void test_pause_before_unlink(const string & path)
{
	const char *ready = getenv("NEWSCACHE_TEST_CLEAN_UNLINK_READY_FD");
	const char *go = getenv("NEWSCACHE_TEST_CLEAN_UNLINK_GO_FD");
	const char *base = strrchr(path.c_str(), '/');
	if (!ready || !go || !base || strncmp(base + 1, ".art", 4) != 0)
		return;
	char byte = 'x';
	if (write(atoi(ready), &byte, 1) != 1 ||
	    read(atoi(go), &byte, 1) != 1)
		_exit(99);
	unsetenv("NEWSCACHE_TEST_CLEAN_UNLINK_READY_FD");
	unsetenv("NEWSCACHE_TEST_CLEAN_UNLINK_GO_FD");
}
#endif

/*
 * The lock a session holds while it puts a new file under the name path:
 * NVNewsgroup::setarticle renames .art<n>.tmp over .art<n> with the
 * group database's exclusive lock, NVcontainer::corrupt moves a database
 * aside (.db over .db.bad) and starts a new one with an exclusive flock
 * on its directory. Returns the descriptor to close, or -1 (no lock).
 * Never both locks at once: corrupt() waits for the new database's lock
 * while it holds the directory's.
 */
static int lock_for_unlink(const string & path)
{
	string::size_type sl = path.rfind('/');
	if (sl == string::npos)
		return -1;
	string dir = path.substr(0, sl);
	int fd, r;

	if (path.compare(sl + 1, 4, ".art") == 0) {
		fd = open((dir + "/.db").c_str(), O_RDWR | O_NOFOLLOW);
		if (fd < 0)
			return -1;
		while ((r = nvflock(fd, NVcontainer::ExclLock,
				    NVcontainer::Block)) != 0 && errno == EINTR)
			;
	} else {
		fd = open(dir.c_str(), O_RDONLY);
		if (fd < 0)
			return -1;
		while ((r = flock(fd, LOCK_EX)) != 0 && errno == EINTR)
			;
	}
	if (r != 0) {
		close(fd);
		return -1;
	}
	return fd;
}

void remove_elements(long long max_blocks, struct cache_statistic *pS)
{
	int isDb;
	char cBuffer[MAXPATHLEN];
	vector<Entry *>::iterator begin, end;

	for (begin=pvector.begin(), end=pvector.end();
	     begin != end; begin++) {
		const string &path = (*begin)->Path;
		if (path.length() >= 3 &&
		    path.compare(path.length() - 3, 3, ".db") == 0) {
			isDb = 1;
		} else {
			isDb = 0;
		}
		if ((*begin)->atime != 0 && _blocks <= max_blocks)
			continue;
		// The run may take long; meanwhile a session can store the
		// article again (a new file under the name) or a group start a
		// new database. That file wasn't scanned: it stays. The check
		// and the unlink are done under the session's lock: between
		// the two, a new file renamed over the name was removed.
		int lfd = -1, r;
		if ((*begin)->known) {
			struct stat now;
			lfd = lock_for_unlink(path);
			if (lstat(path.c_str(), &now) != 0 ||
			    now.st_dev != (*begin)->dev ||
			    now.st_ino != (*begin)->ino) {
				if (lfd >= 0)
					close(lfd);
				slog.p(Logger::Debug) << path
					<< " changed since the scan, kept\n";
				_blocks -= (*begin)->blocks;
				continue;
			}
#ifdef NEWSCACHE_TEST_CLEAN_RACE
			test_pause_before_unlink(path);
#endif
		}
		r = unlink(path.c_str());
		if (lfd >= 0)
			close(lfd);
		if (r == 0) {
			_blocks -= (*begin)->blocks;
			if (!isDb) {
				if ((*begin)->atime != 0) {
					pS->stPurgedArtCnt++;
					pS->stPurgedArtSize += (*begin)->blocks;
				} else {
					pS->stPurgedOutArtCnt++;
					pS->stPurgedOutArtSize += (*begin)->blocks;
				}
			} else {
				pS->stPurgedDbCnt++;
				pS->stPurgedDbSize += (*begin)->blocks;
				strcpy(cBuffer, (*begin)->Path.c_str());
				cBuffer[strlen(cBuffer) - 3] = '\0';
				if (rmdir(cBuffer) == 0) {
					pS->stPurgedDirCnt++;
				} else if (errno == ENOTEMPTY) {
					slog.p(Logger::Debug) 
						<< "Error rmdir "
						<< "(Directory not empty) "
						<< (*begin)->Path << "\n";
				} else {
					slog.p(Logger::Error) 
						<< "Error rmdir "
						<< (*begin)->Path << "\n";
				}
			}
		} else {
			slog.
			    p(Logger::
			      Error) << "Error unlink " << (*begin)->Path << "\n";
		}
	}
}

/*
 * The history's files (<spool>/.history): their blocks count toward
 * SpoolSize. Shards moved aside as damaged (*.bad) go into the purge
 * table, to be removed first; the shards themselves are not purged one
 * by one (see clean_history).
 */
static void scan_history(void)
{
	DIR *d = opendir("./.history");
	struct dirent *f;
	struct stat s;
	char buf[MAXPATHLEN];

	if (!d)
		return;
	while ((f = readdir(d)) != NULL) {
		if (f->d_name[0] == '.')
			continue;
		snprintf(buf, sizeof(buf), "./.history/%s", f->d_name);
		if (lstat(buf, &s) != 0 || !S_ISREG(s.st_mode))
			continue;
		size_t nl = strlen(f->d_name);
		if (nl > 4 && strcmp(f->d_name + nl - 4, ".bad") == 0) {
			pvector.push_back(new Entry(buf, 0, s.st_blocks));
		} else {
			_history_blocks += s.st_blocks;
		}
		_blocks += s.st_blocks;
	}
	closedir(d);
}

/*
 * Removes the history's shard files when History is off (they are
 * unused then), or when the history alone takes more than half of
 * SpoolSize (max_blocks): its files never shrink, and it is only a cache
 * of what the spool holds, filled again as clients read.
 */
static bool clean_history(long long max_blocks)
{
	const char *why;

	if (_history_blocks == 0)
		return false;
	if (!Cfg.HistoryEnabled)
		why = "History is off";
	else if (_history_blocks > max_blocks / 2)
		why = "it takes more than half of SpoolSize";
	else
		return false;
	char buf[32];
	for (int n = 0; n < History::SHARDS; n++) {
		snprintf(buf, sizeof(buf), "./.history/%x", n);
		unlink(buf);
	}
	slog.p(Logger::Notice) << "history: removed ("
	    << (_history_blocks / 2) << "K), " << why << "\n";
	_blocks -= _history_blocks;
	_history_blocks = 0;
	return true;
}

// For History::prune: whether the group still has a database in the
// spool (the current directory); arg caches only positive answers. A group
// found absent can be recreated before History::prune rechecks it.
static bool group_has_db(const string & group, void *arg)
{
	map < string, bool > &dbs = *(map < string, bool > *)arg;
	map < string, bool >::iterator i = dbs.find(group);
	if (i != dbs.end())
		return i->second;
	const char *dir = group2dir(group.c_str());
	struct stat s;
	bool has = dir && stat((string(dir) + "/.db").c_str(), &s) == 0;
	if (has)
		dbs[group] = true;
	return has;
}

// The group's database in the spool directory cpath, or NULL if there is
// none. Throws NoSuchGroupError if NVNewsgroup refuses the group.
static NVNewsgroup *open_spool_group(OverviewFmt *fmt, const char *cpath,
				     const char *ngname)
{
	string dbname = cpath;
	dbname += "/.db";
	struct stat s;
	// not through a symlink: NVNewsgroup would repair a file outside
	if (lstat (dbname.c_str(), &s) != 0 || !S_ISREG(s.st_mode))
		return NULL;
	return new NVNewsgroup (fmt, (const char *) Cfg.SpoolDirectory, ngname);
}

// An .art file of a group directory, as the scan found it.
struct ArtFile {
	string path;
	struct stat st;
};

#ifdef NEWSCACHE_TEST_CLEAN_RACE
// Compiled test fixture: waits after a group's range is read, so that a
// session can store an article meanwhile (tests/e2e TestNewscachecleanRange).
static void test_pause_after_range(void)
{
	const char *ready = getenv("NEWSCACHE_TEST_CLEAN_READY_FD");
	const char *go = getenv("NEWSCACHE_TEST_CLEAN_GO_FD");
	if (!ready || !go)
		return;
	char byte = 'x';
	if (write(atoi(ready), &byte, 1) != 1 ||
	    read(atoi(go), &byte, 1) != 1)
		_exit(99);
	unsetenv("NEWSCACHE_TEST_CLEAN_READY_FD");
	unsetenv("NEWSCACHE_TEST_CLEAN_GO_FD");
}
#endif

void clean(const char *cpath)
{
	VERB(slog.p(Logger::Debug) << "clean(" << cpath << ")\n");
	DIR *d;
	time_t dbatime;
	char buf[MAXPATHLEN];

	struct dirent *f;
	struct stat s;

	if (snprintf(buf, sizeof(buf), "%s/.db", cpath) >= (int) sizeof(buf)) {
		slog.p(Logger::Error) << "path too long, skipped: " << cpath << "\n";
		return;
	}
	// Exception Directories
	if (strcmp(buf, "./.artSpool") == 0)
		return;
	if (strcmp(buf, "./.badArticles") == 0)
		return;
	if (strcmp(buf, "./.history/.db") == 0) {
		scan_history();
		return;
	}
	if (lstat(buf, &s) == 0 && S_ISREG(s.st_mode)) {
		dbatime = MAX(s.st_atime, s.st_mtime);
		pvector.push_back( new Entry (buf, dbatime, s) );
		_blocks += s.st_blocks;
		--dbatime;
	} else {
		dbatime = 0;
	}

	if ((d = opendir(cpath)) == NULL) {
		slog.p(Logger::Error) << "cannot open " << cpath << "\n";
		return;
	}
	while ((f = readdir(d)) != NULL) {
		if (strcmp(f->d_name, ".") == 0)
			continue;
		if (strcmp(f->d_name, "..") == 0)
			continue;
		if (snprintf(buf, sizeof(buf), "%s/%s", cpath, f->d_name) >=
		    (int) sizeof(buf)) {
			slog.p(Logger::Error) << "path too long, skipped: "
				<< cpath << "/" << f->d_name << "\n";
			continue;
		}
		// lstat, and only real directories: a symlink would lead
		// out of the spool (files there deleted) or into a loop
		if (lstat(buf, &s) == 0) {
			if (S_ISDIR(s.st_mode))
				clean(buf);
			else if (S_ISLNK(s.st_mode))
				slog.p(Logger::Notice)
					<< "not following symlink " << buf << "\n";
		} else {
			slog.
			    p(Logger::
			      Error) << "cannot stat " << buf << "\n";
		}
	}
	closedir(d);

	// sanity check
	string ngname (cpath);
	// ./be/comp/os/unix -> be.comp.os.unix
	ngname.replace (0, 2, "");
	int i;
	while ((i=ngname.find ("/")) != -1) {
		ngname[i] = '.';
	}
	NVNewsgroup *ng = NULL;
	OverviewFmt *fmt = new OverviewFmt ();
	artnum_t firstnr = 0;
	artnum_t lastnr = 0;
	// A corrupt database is moved to .db.bad when it is opened (and a
	// new one started), see NVcontainer::corrupt: opened before the
	// directory is read, so that the .db.bad is found there. Any other
	// error: log it and treat the group as having no database, so its
	// .art files go. (NC_CATCH_ALL rethrew it, and the run ended with
	// SIGABRT.)
	if (dbatime != 0) {
		try {
			ng = open_spool_group(fmt, cpath, ngname.c_str());
		} catch (const NoSuchGroupError &e) {
		} catch (const Error &e) {
			slog.p(Logger::Error) << "cannot read the database of "
				<< ngname << ": " << e._errtext << "\n";
			delete ng;
			ng = NULL;
		}
	}

	if ((d = opendir(cpath)) == NULL) {
		slog.p(Logger::Error) << "cannot reopen " << cpath << "\n";
		delete ng;
		delete fmt;
		return;
	}
	// The .art files first, then the group's range: an article stored
	// while the directory is read has a number the range then covers.
	// (Read the other way round, a big article stored just after the
	// range was taken was outside it, and removed at the end of the run.)
	vector < ArtFile > arts;
	while ((f = readdir(d)) != NULL) {
		if (snprintf(buf, sizeof(buf), "%s/%s", cpath, f->d_name) >=
		    (int) sizeof(buf)) {
			slog.p(Logger::Error) << "path too long, skipped: "
				<< cpath << "/" << f->d_name << "\n";
			continue;
		}
		// lstat: a symlinked file is neither counted nor removed
		if (lstat(buf, &s) == 0) {
			size_t nl = strlen(f->d_name);
			if (S_ISREG(s.st_mode) && nl > 4 &&
			    strcmp(f->d_name + nl - 4, ".bad") == 0) {
				// a database moved aside as corrupt: kept for a
				// look until this run, removed first
				pvector.push_back (new Entry(buf, 0, s));
				_blocks += s.st_blocks;
			} else if (S_ISREG(s.st_mode) && dbatime != 0 &&
			    strncmp(f->d_name, ".art", 4) == 0) {
				ArtFile a;
				a.path = buf;
				a.st = s;
				arts.push_back(a);
			}
		} else {
			slog.
			    p(Logger::
			      Error) << "cannot stat " << buf << "\n";
		}
	}
	closedir(d);
	if (ng != NULL) {
		try {
			ng->spoolrange(&firstnr, &lastnr);
		} catch (const Error &e) {
			slog.p(Logger::Error) << "cannot read the database of "
				<< ngname << ": " << e._errtext << "\n";
			firstnr = lastnr = 0;
			delete ng;
			ng = NULL;
		}
	}
	int have_db = ng != NULL;
	delete ng;
	delete fmt;
	if (dbatime == 0)
		return;
#ifdef NEWSCACHE_TEST_CLEAN_RACE
	if (have_db)
		test_pause_after_range();
#endif
	artnum_t nr;
	const char *end;
	for (size_t k = 0; k < arts.size(); k++) {
		const char *name = strrchr(arts[k].path.c_str(), '/') + 1;
		const struct stat &st = arts[k].st;
		time_t at = MAX(st.st_atime, st.st_mtime);
		if (dbatime <= at)
			at = dbatime;
		// .art<n>.tmp: a file NewsCache was writing when
		// it crashed (see NVNewsgroup::setarticle); it
		// goes by its age, as an article file does
		if (!parse_artnum(name + 4, &nr, &end) ||
		    (*end && strcmp(end, ".tmp") != 0)) {
			slog.p(Logger::Error) << "error "
				<< "parsing " << name << "\n";
		} else if (!have_db || nr < firstnr || nr > lastnr) {
			at = 0;
		}
		pvector.push_back (new Entry(arts[k].path.c_str(), at, st));
		_blocks += st.st_blocks;
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
"       -s --statistic\n"\
"              Show statistic informations.\n"\
"\n"\
"       -p --print-purgetable\n"\
"              Print the sorted purge table.\n"\
"\n"\
"       -t --try\n"\
"              Try, not really removing files.\n"\


int main(int argc, char **argv)
{
#ifndef WITH_SYSLOG
	char logfile[MAXPATHLEN];
#endif
	char conffile[MAXPATHLEN];
	int c, aerr = 0;
	struct cache_statistic Stat;
	int nice;
	int stat=0;
	int try_flag = 0;
	char p_flag=0;
	vector<Entry *>::iterator begin, end;

	memset ((void *) &Stat, 0, sizeof (struct cache_statistic));

	snprintf(conffile, sizeof(conffile), "%s/newscache.conf", SYSCONFDIR);

	cmnd = argv[0];
	while (1) {
#ifdef HAVE_GETOPT_H
		int option_index = 0;
		static struct option long_options[] = {
			{"version", 0, 0, 'v'},
			{"help", 0, 0, 'h'},
			{"configuration", 1, 0, 'c'},
			{"statistic", 0, 0, 's'},
			{"try", 0, 0, 't'},
			{"print-purgetable", 0, 0, 'p'},
			{0, 0, 0, 0}
		};

		c = getopt_long (argc, argv, "vhc:stp", long_options,
				&option_index);
#else
		c = getopt (argc, argv, "vhc:stp");
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

			case 'c':
				if (snprintf(conffile, sizeof(conffile), "%s", optarg) >=
				    (int) sizeof(conffile)) {
					cerr << cmnd << ": configuration file name is too long (at most "
					     << sizeof(conffile) - 1 << " characters)\n";
					exit(1);
				}
				break;

			case 's':
				stat = 1;
				break;

			case 't':
				try_flag = 1;
				break;
			
			case 'p':
				p_flag = 1;
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

	try {
		Cfg.read(conffile);
//    strcpy(nntp_hostname,Cfg.Hostname);
	}
	catch(const IOError & io) {
		if (io._errno == -1)
			cerr << "unexpected EOF in " << conffile << "\n";
		else
			cerr << io._errtext << ": " << strerror(io._errno) << "\n";
		exit(2);
	}
	catch(const SyntaxError & se) {
		cerr << se._errtext << "\n";
		exit(2);
	}

#ifdef WITH_SYSLOG
	slog.open(PACKAGE, LOG_NDELAY | LOG_PID, LOG_NEWS);
#else
	if (snprintf(logfile, sizeof(logfile), "%s/newscache_newscacheclean.log",
		     Cfg.LogDirectory) >= (int) sizeof(logfile)) {
		cerr << "LogDirectory is too long\n";
		exit(1);
	}
	slog.open (logfile);
#endif
	slog.p(Logger::Info) << "cleaning spool directory\n";


	/* set nice value for newscacheclean */
#ifdef HAVE_SETPRIORITY
	errno = 0;
	// -1 is also a valid priority: errno tells an error
	nice = getpriority(PRIO_PROCESS, 0);
	if (nice == -1 && errno != 0) {
		slog.
		    p(Logger::
		      Error) << "getpriority failed: " << strerror(errno)
		    << "\n";
	} else {
		nice += Cfg.NiceClean;
		if (setpriority(PRIO_PROCESS, 0, nice) == -1)
			slog.
			    p(Logger::
			      Error) << "setpriority failed: " <<
			    strerror(errno) << "\n";
	}
#endif

	if (chdir(Cfg.SpoolDirectory) < 0) {
		slog.
		    p(Logger::
		      Error) <<
		    "cannot change to spool directory -- exiting\n";
		exit(1);
	}
	if (getuid() == CONF_UIDROOT)
		setugid(Cfg.Username, Cfg.Groupname);
	// One run at a time: runs that overlap (cron) computed the purge
	// from the same sizes, and together removed about twice what was
	// needed. The lock goes with the process. (Not with -t, which
	// removes nothing and leaves the spool as it is.)
	string lockname = string(Cfg.SpoolDirectory) + "/.newscacheclean.lock";
	ObjLock *runlock = NULL;
	try {
		if (!try_flag)
			runlock = new ObjLock(lockname);
		if (runlock && runlock->lockExNoBlk() != ObjLock::locked) {
			slog.p(Logger::Notice) << "another newscacheclean is "
			    "running (" << lockname << "), exiting\n";
			cerr << cmnd << ": another newscacheclean is running ("
			    << lockname << ")\n";
			exit(3);
		}
	}
	catch(const SystemError & e) {
		slog.p(Logger::Error) << "cannot lock " << lockname << ": "
		    << strerror(e._errno) << "\n";
		cerr << cmnd << ": cannot lock " << lockname << ": "
		    << strerror(e._errno) << "\n";
		exit(1);
	}
	if (Cfg.HistoryEnabled)
		History::setspooldir(Cfg.SpoolDirectory);
	clean(".");
	sort (pvector.begin(), pvector.end(), sort_entries);
	if (p_flag) {
		cout << "---------------- Purge table ----------------" << endl;
		for (begin=pvector.begin(), end=pvector.end();
		     begin != end; begin++) {
			(*begin)->print (cout);
		}
		cout << "---------------- Purge table ----------------" << endl;
	}
	if (stat)
		collect_statistics(&Stat);
	if (!try_flag) {
		// in 512-byte blocks, as st_blocks counts
		long long max_blocks = Cfg.SpoolSize * 2;
		bool hist_removed = clean_history(max_blocks);
		remove_elements(max_blocks, &Stat);
		// the history's entries of groups whose database is gone
		if (!hist_removed) {
			map < string, bool > dbs;
			long n = History::prune(group_has_db, &dbs);
			if (n > 0)
				slog.p(Logger::Info) << "history: " << n
				    << " entries of removed groups dropped\n";
		}
	}
	if (stat) {
		print_statistic(cout, &Stat);
	}

	slog.p(Logger::Info) << "|SpoolDirectory|="
	    << (_blocks / 2) << "K\n";
	for (begin=pvector.begin(), end=pvector.end(); begin != end; begin++)
		delete *begin;
	pvector.clear();
	delete runlock;
	return 0;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
