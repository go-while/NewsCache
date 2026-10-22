#ifndef _GroupInfo_h_
#define _GroupInfo_h_

#include <iostream>
#include <stddef.h>

#include "artnum.h"
#include "NSError.h"
#include "NVcontainer.h"

/**
 * \class GroupInfo
 * \author Thomas Gschwind
 *
 * Stored as is (getraw/setraw) in the active database, so the members
 * are part of its file format: name last, with no padding before it. The
 * article numbers were unsigned int up to 1.3.21 (file .active); since
 * then they are 64-bit (file .active64, see NVActiveDB::convert_v1).
 *
 * \bug Documentation is missing.
 */
class GroupInfo {
	nvtime_t _ctime;
	nvtime_t _mtime;
	artnum_t _fst, _lst, _n;
	char _flag;
	char _name[512];

  public:
	GroupInfo() {
		init();
	}
	GroupInfo(const char *gd) {
		_name[sizeof(_name) - 1] = '\0';
		set(gd);
	}

	void init() {
		_ctime = _mtime = 0;
		_name[0] = _name[sizeof(_name) - 1] = '\0';
		_flag = 'y';
	}

	// All data-types stored in NVcontainers must have a set- and getraw
	// method
	void setraw(const char *buf, unsigned int bufsz) {
		if (bufsz > sizeof(_ctime) + sizeof(_mtime) +
		    sizeof(_fst) + sizeof(_lst) + sizeof(_n) +
		    sizeof(_flag) + sizeof(_name))
			throw Error("GroupInfo(24230): setraw buf too big",
				    ERROR_LOCATION);
		memcpy(this, buf, bufsz);
	}

	void getraw(const char **buf, unsigned int *bufsz) {
		static_assert(offsetof(GroupInfo, _name) ==
			      sizeof(_ctime) + sizeof(_mtime) + sizeof(_fst) +
			      sizeof(_lst) + sizeof(_n) + sizeof(_flag),
			      "GroupInfo is stored as is in the active database");
		*buf = (const char *) this;
		*bufsz = sizeof(_ctime) + sizeof(_mtime) +
		    sizeof(_fst) + sizeof(_lst) + sizeof(_n) +
		    sizeof(_flag) + strlen(_name) + 1;
	}

	void set(const char *gd) {
		const char *p = gd, *e;
		char *q = _name;
		unsigned int i = 0;

		while (i < sizeof(_name) && !isspace(*p)) {
			*q++ = *p++;
			++i;
		}
		if (i == sizeof(_name))
			throw Error("Name of newsgroup too long",
				    ERROR_LOCATION);
		*q = '\0';

		while (isspace((unsigned char) *p))
			p++;
		if (!parse_artnum(p, &_lst, &e))
			throw NSError("gd not in form name int int flag",
				      ERROR_LOCATION);
		while (isspace((unsigned char) *e))
			e++;
		if (!parse_artnum(e, &_fst, &p))
			throw NSError("gd not in form name int int flag",
				      ERROR_LOCATION);

		if (_fst > _lst)
			_n = 0;
		else
			_n = _lst - _fst + 1;

		while (isspace(*p))
			p++;
		if (*p)
			_flag = *p;
		else
			_flag = 'y';

		nvtime(&_mtime);
	}

	void set(const char *group, artnum_t fst, artnum_t lst, artnum_t n,
		 char fl = 'y') {
		size_t len = strlen(group);
		if (len >= sizeof(_name))
			throw Error("Name of newsgroup too long",
				    ERROR_LOCATION);
		memcpy(_name, group, len + 1);
		setfln(fst, lst, n, fl);
	}

	void setfln(artnum_t first, artnum_t last, artnum_t n,
		    char fl = 'y') {
		nvtime_t tm;
		_fst = first;
		_lst = last;
		_n = n;
		if (fl)
			_flag = fl;
		nvtime(&tm);
		_mtime = tm;
	}
	void setflag(char fl) {
		_flag = fl;
	}

	void setctime(nvtime_t ctime) {
		_ctime = ctime;
	}
	void setmtime(nvtime_t mtime) {
		_mtime = mtime;
	}

	nvtime_t ctime() const {
		return _ctime;
	}
	nvtime_t mtime() const {
		return _mtime;
	}
	const char *name() const {
		return _name;
	}
	artnum_t first() const {
		return _fst;
	}
	artnum_t last() const {
		return _lst;
	}
	artnum_t n() const {
		return _n;
	}
	char flags() const {
		return _flag;
	}

	friend std::ostream & operator <<(std::ostream & os,
									  const GroupInfo & gd) {
		os << gd._name << ' ' << gd._lst << ' ' << gd.
		    _fst << ' ' << gd._flag;
		return os;
	}
};

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
