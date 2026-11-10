#include "RNewsgroup.h"

// Only remembers the numbers; records are fetched when asked for. (This
// fetched the overview of the whole group, from number 1.)
void RNewsgroup::setsize(artnum_t f, artnum_t l)
{
	if (l > ARTNUM_LIMIT)
		l = ARTNUM_LIMIT;
	if (f != _first || l != _last) {
		if (l < _last)
			slog.p(Logger::Notice) << "watermark(s) decreased?!?\n";
		_OverviewDB.clear();
		_cf = 1;
		_cl = 0;
		_first = f;
		_last = l;
	}
}

// Fetches the records of the chunk that holds nbr, unless they are
// already there. Returns 0 if nbr isn't in the group; fetching failures
// reach the command loop rather than becoming an empty range.
int RNewsgroup::load(artnum_t nbr)
{
	artnum_t a, b;

	if (nbr < _first || nbr > _last || nbr == 0)
		return 0;
	if (_cf <= nbr && nbr <= _cl)
		return 1;
	a = nbr / OVERVIEW_CHUNK * OVERVIEW_CHUNK;
	b = chunk_last(nbr);
	if (a < _first)
		a = _first;
	if (b > _last)
		b = _last;
	_OverviewDB.clear();
	_cf = a;
	_cl = b;
	try {
		_RemoteServer->overviewdb(this, a, b);
	}
	catch(Error & e) {
		slog.p(Logger::Error) << "overview of " << name() << " "
		    << a << "-" << b << " failed: " << e._errtext << "\n";
		_OverviewDB.clear();
		_cf = 1;
		_cl = 0;
		throw UpstreamReplyError("overview of " + std::string(name()) +
					 ": " + e._errtext);
	}
	catch(...) {
		_OverviewDB.clear();
		_cf = 1;
		_cl = 0;
		throw;
	}
	return 1;
}

const char *RNewsgroup::getover(artnum_t nbr)
{
	if (!load(nbr))
		return NULL;
	iterator over = _OverviewDB.find(nbr);
	if (over != _OverviewDB.end())
		return over->second.c_str();
	return NULL;
}

void check_walk(const WalkGuard & guard, const char *group, artnum_t at)
{
	const char *why = guard.stop();

	if (!why)
		return;
	if (client_gone())
		slog.p(Logger::Notice) << group << ": " << why
		    << ", giving up at " << at << "\n";
	else
		slog.p(Logger::Error) << group << ": no article found for "
		    << LISTGROUP_IDLE_SECONDS << " s, giving up at " << at << "\n";
	throw UpstreamReplyError(std::string(group) + ": " + why);
}

bool probe_upstream(RServer * srvr, const char *group, artnum_t f, artnum_t l,
		    bool down, const WalkGuard & guard, artnum_t * at)
{
	try {
		return srvr->probelisted(group, f, l, down, guard, at);
	}
	catch(const UpstreamReplyError &) {
		throw;	// (check_walk, logged)
	}
	catch(const Error & e) {
		slog.p(Logger::Error) << "listgroup " << group << " " << f << "-"
		    << l << " failed: " << e._errtext << "\n";
		throw UpstreamReplyError("listgroup " + std::string(group) + ": " +
					 e._errtext);
	}
}

/*
 * Prints the overview records, a header's lines or the numbers of [f,l],
 * one chunk after the other as the news server sends them. After
 * WALK_EMPTY_CHUNKS chunks in a row without a record it asks the news
 * server where the next articles are (probe), instead of fetching chunk
 * after chunk through millions of empty numbers. It gives up when the
 * client's connection was reset or nothing was found for too long
 * (WalkGuard).
 */
void RNewsgroup::walk(std::ostream & os, int what, const char *header,
		      artnum_t f, artnum_t l, const char *pat)
{
	std::string xheader, fld;
	WalkGuard guard;
	artnum_t a, n;
	unsigned int empty = 0;	// chunks in a row without a record
	bool found, probing = true;

	if (what == walk_header)
		xheader = headerfield(header);
	if (f < _first)
		f = _first;
	if (f == 0)
		f = 1;	// no article 0: load(0) fails, its chunk was skipped
	if (l > _last)
		l = _last;
	for (a = f; a <= l && os.good();) {
		check_walk(guard, name(), a);
		found = false;
		if (load(a)) {
			iterator i = _OverviewDB.lower_bound(a), end =
			    _OverviewDB.end();
			for (; i != end && i->first <= l; ++i) {
				found = true;
				switch (what) {
				case walk_over:
					os << i->second << "\r\n";
					break;
				case walk_header:
					try {
						fld = _OverviewFormat->
						    getfield(i->second.c_str(),
							     xheader.c_str(), 0);
					}
					catch(NoSuchFieldError & nsfe) {
						headerline(os, i->first, NULL, pat);
						break;
					}
					headerline(os, i->first, &fld, pat);
					break;
				case walk_list:
					os << i->first << "\r\n";
					break;
				}
			}
		}
		// next chunk; 0 after the last one (the addition used to
		// wrap around to a small number near ARTNUM_MAX)
		n = next_chunk(a);
		if (n == 0 || n > l)
			break;
		if (found) {
			guard.progress();
			empty = 0;
		}
		if (found || ++empty < WALK_EMPTY_CHUNKS || !probing) {
			a = n;
			continue;
		}
		// (chunk by chunk from a if it can't tell)
		probing = probe(n, l, false, guard, &a);
		if (a == 0)
			break;
	}
}

void RNewsgroup::printoverdb(std::ostream & os, artnum_t f, artnum_t l)
{
	walk(os, walk_over, NULL, f, l);
}

void RNewsgroup::printheaderdb(std::ostream & os, const char *header,
			       artnum_t f, artnum_t l, const char *pat)
{
	walk(os, walk_header, header, f, l, pat);
}

void RNewsgroup::printlistgroup(std::ostream & os, artnum_t f, artnum_t l)
{
	artnum_t next;

	if (f < _first)
		f = _first;
	if (f == 0)
		f = 1;	// no article 0: load(0) fails, its chunk was skipped
	if (l > _last)
		l = _last;
	if (f > l)
		return;
	next = f;
	try {
		_RemoteServer->printlistgroup(_NewsgroupName, os, f, l, &next);
		return;
	}
	catch(ResponseError & e) {
		// no LISTGROUP (with a range): the overview records tell
		if (!listgroup_unsupported(e))
			throw UpstreamReplyError("listgroup " + std::string(name()) +
						 ": " + e._got);
	}
	catch(Error & e) {
		slog.p(Logger::Error) << "listgroup " << name() << " failed: "
		    << e._errtext << "\n";
		throw UpstreamReplyError("listgroup " + std::string(name()) +
					 ": " + e._errtext);
	}
	// the numbers below next were listed already
	walk(os, walk_list, NULL, next, l);
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
