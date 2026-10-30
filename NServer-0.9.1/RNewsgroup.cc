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

void RNewsgroup::printoverdb(std::ostream & os, artnum_t f, artnum_t l)
{
	artnum_t a;

	if (f < _first)
		f = _first;
	if (f == 0)
		f = 1;	// no article 0: load(0) fails, its chunk was skipped
	if (l > _last)
		l = _last;
	for (a = f; a <= l && os.good();) {
		if (load(a)) {
			iterator i = _OverviewDB.lower_bound(a), end =
			    _OverviewDB.end();
			for (; i != end && i->first <= l; ++i)
				os << i->second << "\r\n";
		}
		// next chunk ("a = _cl + 1" ends at l == ARTNUM_MAX too)
		// next chunk; 0 after the last one (the addition used to
		// wrap around to a small number near ARTNUM_MAX)
		a = next_chunk(a);
		if (a == 0)
			break;
	}
}

void RNewsgroup::printheaderdb(std::ostream & os, const char *header,
			       artnum_t f, artnum_t l, const char *pat)
{
	std::string xheader = headerfield(header), fld;
	artnum_t a;

	if (f < _first)
		f = _first;
	if (f == 0)
		f = 1;	// no article 0: load(0) fails, its chunk was skipped
	if (l > _last)
		l = _last;
	for (a = f; a <= l && os.good();) {
		if (load(a)) {
			iterator i = _OverviewDB.lower_bound(a), end =
			    _OverviewDB.end();
			for (; i != end && i->first <= l; ++i) {
				try {
					fld = _OverviewFormat->
					    getfield(i->second.c_str(),
						     xheader.c_str(), 0);
				}
				catch(NoSuchFieldError & nsfe) {
					headerline(os, i->first, NULL, pat);
					continue;
				}
				headerline(os, i->first, &fld, pat);
			}
		}
		// next chunk; 0 after the last one (the addition used to
		// wrap around to a small number near ARTNUM_MAX)
		a = next_chunk(a);
		if (a == 0)
			break;
	}
}

void RNewsgroup::printlistgroup(std::ostream & os, artnum_t f, artnum_t l)
{
	artnum_t a;

	if (f < _first)
		f = _first;
	if (f == 0)
		f = 1;	// no article 0: load(0) fails, its chunk was skipped
	if (l > _last)
		l = _last;
	if (f > l)
		return;
	try {
		_RemoteServer->printlistgroup(_NewsgroupName, os, f, l,
					      f <= _first && l >= _last);
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
	for (a = f; a <= l && os.good();) {
		if (load(a)) {
			iterator i = _OverviewDB.lower_bound(a), end =
			    _OverviewDB.end();
			for (; i != end && i->first <= l; ++i)
				os << i->first << "\r\n";
		}
		// next chunk; 0 after the last one (the addition used to
		// wrap around to a small number near ARTNUM_MAX)
		a = next_chunk(a);
		if (a == 0)
			break;
	}
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
