#include <time.h>
#include <utime.h>
#include <unistd.h>
#include <fcntl.h>

#include <stdio.h>
#include <string.h>

#include <iostream>
#include <fstream>
#include <string>

#include "util.h"
#include "NSError.h"
#include "NVActiveDB.h"
#include "readline.h"
#include "ReplyBudget.h"
#include "sockstream.h"

using namespace std;

// protected:
const char *NVActiveDB::recname(Record * r)
{
	size_t off = GroupInfo::rawnameoffset();

	if (r->szdata <= off || r->szdata > sizeof(GroupInfo) ||
	    !memchr(r->datap() + off, '\0', r->szdata - off))
		corrupt("bad record in the active database");
	return r->datap() + off;
}

unsigned long NVActiveDB::hash(const char *strg)
{
	unsigned long h = 1;
	const char *p = strg;
	while (p[0]) {
		h = (h * (p[0]) + p[1]) % NVActiveDB_HASHSIZE;
		if (!p[1])
			break;
		p += 2;
	}
	return h;
}

void NVActiveDB::sset(GroupInfo & gi, int flags)
{
	//   GroupInfo cg;
	const char *gidata;
	unsigned int gisz;
	Record *head = o2r(hashtab[hash(gi.name())]);
	Record *r = head;
	unsigned long steps = 0;

	while (r) {
		chain_step(&steps);
		r = o2r(r->next);
		if (!r)
			break;	// lists are circular; 0 only in a corrupt file

		// The first approach is the cleaner one since it uses 
		// GroupInfo's setraw function. However, this involves 
		// a copy operation
//     cg.setraw(r->datap(),r->szdata);
//     if(strcmp(gi.name(),cg.name())==0) {
//       gi.setctime(cg.ctime());
		// The second approach assumes that the GroupInfo
		// data is stored in the same layout in the file.
		// The last (sizeof(GroupInfo)-r->szdata) bytes don't
		// matter. These are junk anyways.
		if (strcmp(recname(r), gi.name()) == 0) {
			gi.setctime(((GroupInfo *) r->datap())->ctime());
			gi.getraw(&gidata, &gisz);
			if (gisz != r->szdata) {
				VERB(slog.
				     p(Logger::
				       Error) <<
				     "NVActiveDB::set: Size of new record not equal original record size. Should have been impossible.\n");
			} else {
				memcpy(r->datap(), gidata, gisz);
			}
			break;
		}
		if (r == head)
			r = NULL;
	}

	if (!r) {
		if (flags & update_only) {
			// Should we throw an exception here?!?
			VERB(slog.
			     p(Logger::
			       Error) <<
			     "NVActiveDB::set: Cannot find requested record\n");
		} else {
			const char *buf;
			unsigned int bufsz;
			nvtime_t now;
			nvtime(&now);
			gi.setctime(now);
			gi.getraw(&buf, &bufsz);
			NVHash::add(hash(gi.name()), buf, bufsz);
		}
	}
}

void NVActiveDB::add(GroupInfo & gi)
{
	VERB(slog.p(Logger::Debug) << "NVActiveDB::add(GroupInfo &gi)\n)");
	const char *buf;
	unsigned int bufsz;
	gi.getraw(&buf, &bufsz);
	NVHash::add(hash(gi.name()), buf, bufsz);
}

int NVActiveDB::get(const char *group, GroupInfo * gi)
{
	VERB(slog.
	     p(Logger::Debug) << "NVActiveDB::get(" << group << ",&gi)\n");
	lock(ShrdLock);
	Record *head = o2r(hashtab[hash(group)]);
	Record *r = head;
	unsigned long steps = 0;

	while (r) {
		chain_step(&steps);
		r = o2r(r->next);
		if (!r)
			break;	// lists are circular; 0 only in a corrupt file

		// The first approach is the cleaner one since it uses 
		// GroupInfo's setraw function. However, this involves 
		// a copy operation
//     gi->setraw(r->datap(),r->szdata);
//     if(strcmp(gi->name(),group)==0) break;

		// The second approach assumes that the GroupInfo
		// data is stored in the same layout in the file.
		// The last (sizeof(GroupInfo)-r->szdata) bytes don't
		// matter. These are junk anyways.
		if (strcmp(recname(r), group) == 0) {
			gi->setraw(r->datap(), r->szdata);
			break;
		}
		if (r == head)
			r = NULL;
	}
	lock(UnLock);

	if (!r)
		return -1;
	return 0;
}

int NVActiveDB::hasgroup(const char *group)
{
	VERB(slog.
	     p(Logger::Info) << "NVActiveDB::hasgroup(" << group << ")\n");
	lock(ShrdLock);
	Record *head = o2r(hashtab[hash(group)]);
	Record *r = head;
	unsigned long steps = 0;

	while (r) {
		chain_step(&steps);
		r = o2r(r->next);
		if (!r)
			break;	// lists are circular; 0 only in a corrupt file

		// The first approach is the cleaner one since it uses 
		// GroupInfo's setraw function. However, this involves 
		// a copy operation
//     gi->setraw(r->datap(),r->szdata);
//     if(strcmp(gi->name(),group)==0) break;

		// The second approach assumes that the GroupInfo
		// data is stored in the same layout in the file.
		// The last (sizeof(GroupInfo)-r->szdata) bytes don't
		// matter. These are junk anyways.
		if (strcmp(recname(r), group) == 0) {
			break;
		}
		if (r == head)
			r = NULL;
	}
	lock(UnLock);

	if (!r)
		return 0;
	return 1;
}

void NVActiveDB::read(istream & is, const char *filter, int flags,
		      unsigned int limit)
{
	VERB(slog.
	     p(Logger::Debug) << "NVActiveDB::read(&is,*filter,flags)\n");

	GroupInfo gd;
	string line;
	time_t now;
	bool done = false;
	ReplyBudget budget(ACTIVE_REPLY_ROWS, ACTIVE_REPLY_BYTES, ACTIVE_REPLY_SECONDS);

	NVLock guard(*this, NVcontainer::ExclLock);
	if (flags & F_CLEAR)
		clear();
	while (!is.eof()) {
		// Each line has the following form
		// GroupName Last First Flags
		size_t wire_bytes = 0;
		nlreadline(is, line, 0, NNTP_MAXLINE, &wire_bytes);
		if (budget.expired())
			throw ReplyLimitError(budget);
		if (!is.good()) {
			sockstream *socket = dynamic_cast<sockstream *>(&is);
			if (socket && socket->reply_expired())
				throw ReplyLimitError(budget, true);
			break;
		}
		if (line == ".") {
			if (!budget.finish(wire_bytes))
				throw ReplyLimitError(budget);
			done = true;
			break;
		}
		if (!budget.add(wire_bytes))
			throw ReplyLimitError(budget);

		if (!filter || matchgroup(filter, line.c_str()) > 0) {
			// Add group. Skip a line we can't parse (e.g. a group
			// name longer than GroupInfo allows) instead of giving
			// up: that left the rest of the list unread on the
			// connection, the database locked and no groups at all.
			try {
				gd.set(line.c_str());
			} catch (const Error &e) {
				slog.p(Logger::Warning)
				    << "active list: skipping a line ("
				    << e._errtext << "): " << line.substr(0, 80)
				    << (line.size() > 80 ? "..." : "") << "\n";
				continue;
			}
			if (flags & F_STORE_READONLY)
				gd.setflag('n');
			// Keep the configured newest-article limit in the active DB,
			// just as GROUP replies do in RServer::selectgroup().
			if (limit && gd.last() >= gd.first() &&
			    gd.last() - gd.first() >= limit)
				gd.setfln(gd.last() - limit + 1, gd.last(), limit,
					  gd.flags());
			// The caller closes an unread reply on storage failure.
			sset(gd);
		}
	}
	// A list cut off (the connection ended before the ".") isn't
	// complete: without the new mtime it is fetched again next time.
	if (!done)
		throw SystemError("active list cut off by the news server", -1,
				  ERROR_LOCATION);
	time(&now);
	setmtime(now);
}

namespace {
// A GroupInfo as NewsCache 1.3.21 and older stored it (file .active).
struct GroupInfoV1 {
	nvtime_t ctime;
	nvtime_t mtime;
	unsigned int fst, lst, n;
	char flag;
	char name[512];
};
}

// Whether fn can be opened, i.e. exists for us.
static bool can_open(const char *fn)
{
	int fd = ::open(fn, O_RDONLY);
	if (fd < 0)
		return false;
	::close(fd);
	return true;
}

int NVActiveDB::convert_v1(const char *oldname, const char *newname)
{
	int groups = 0;
	string tmpname = string(newname) + ".tmp";
	string v1name = string(oldname) + ".v1";

	// The spool belongs to the news user, and every NewsCache process
	// comes through here before it opens newname, so the names can only
	// change under the lock on oldname below.
	if (can_open(newname) || !can_open(oldname))
		return 0;

	try {
		NVHash old(oldname, NVActiveDB_HASHSIZE);

		// Processes that start at the same time wait here; the
		// first one converts, the others find newname.
		old.lock(NVcontainer::ExclLock);
		try {
			if (!can_open(newname)) {
				nvtime_t mtime;
				unlink(tmpname.c_str());
				NVActiveDB db(tmpname.c_str());
				db.lock(NVcontainer::ExclLock);
				{
					NVHashIter it(old);
					const char *buf;
					unsigned int sz;
					for (it.first(); it.valid(); it.next()) {
						GroupInfoV1 v;
						GroupInfo gi;

						memset(&v, 0, sizeof(v));
						it.data(&buf, &sz);
						memcpy(&v, buf, sz < sizeof(v) ? sz : sizeof(v));
						v.name[sizeof(v.name) - 1] = '\0';
						if (!v.name[0])
							continue;
						gi.set(v.name, v.fst, v.lst, v.n,
						       v.flag ? v.flag : 'y');
						gi.setctime(v.ctime);
						gi.setmtime(v.mtime);
						db.add(gi);
						groups++;
					}
				}
				old.getmtime(&mtime);
				db.setmtime(mtime, 1);
				db.lock(NVcontainer::UnLock);
				db.close();
				if (rename(tmpname.c_str(), newname) < 0)
					throw SystemError("cannot rename the converted active database",
							  errno, ERROR_LOCATION);
				// Out of the way, or deleting newname (see the
				// FAQ) would bring the old groups back.
				if (rename(oldname, v1name.c_str()) < 0)
					slog.p(Logger::Warning) << "cannot rename "
						<< oldname << " to " << v1name << "\n";
				slog.p(Logger::Notice) << "converted " << groups
					<< " groups from " << oldname << " to "
					<< newname << "\n";
			}
		}
		catch(...) {
			old.lock(NVcontainer::UnLock);
			throw;
		}
		old.lock(NVcontainer::UnLock);
	}
	catch(const Error & e) {
		slog.p(Logger::Error) << "cannot convert " << oldname
			<< " to " << newname << " (" << e._errtext
			<< "), starting with an empty active database\n";
		unlink(tmpname.c_str());
		return -1;
	}
	return groups;
}

void NVActiveDB::write(ostream & os,
		       nvtime_t ctime, int mode, const char *filter)
{
	VERB(slog.
	     p(Logger::Debug) << "NVActiveDB::write(&os,ctime,...)\n");

	NVHashIter nvhi(*this);
	GroupInfo gd;
	const char *buf;
	unsigned int bufsz;

	for (nvhi.first(); nvhi.valid(); nvhi.next()) {
		nvhi.data(&buf, &bufsz);
		gd.setraw(buf, bufsz);
		if (((!filter) ||
		     ((matchgroup(filter, gd.name()) > 0))) &&
		    ctime < gd.ctime()) {
			if (mode == m_active)
				os << gd << "\r\n";
			else
				os << gd.name() << " " << gd.
				    ctime() << " news\r\n";
			if (!os.good()) {
				throw SystemError("Cannot write GroupList",
						  errno, ERROR_LOCATION);
			}
		}
	}			/* for */
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
