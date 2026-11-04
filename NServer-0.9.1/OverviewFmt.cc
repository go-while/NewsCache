#include "OverviewFmt.h"
#include "sockstream.h"

using namespace std;

void OverviewFmt::readxoin(istream & is, ReplyBudget *budget)
{
	string field;
	int fn = 0, i;

	for (i = 0; i < _osz; i++) {
		_trans[i].trans = -1;
	}

	for (;;) {
		size_t wire_bytes = 0;
		nlreadline(is, field, 0, NNTP_MAXLINE, &wire_bytes);
		if (budget) {
			if (budget->expired())
				throw ReplyLimitError(*budget);
			if (!is.good()) {
				sockstream *socket = dynamic_cast<sockstream *>(&is);
				if (socket && socket->reply_expired())
					throw ReplyLimitError(*budget, true);
			}
		}
		if (field == "." || is.eof()) {
			if (budget && field == "." && !budget->finish(wire_bytes))
				throw ReplyLimitError(*budget);
			break;
		}
		if (budget && !budget->add(wire_bytes))
			throw ReplyLimitError(*budget);
		// RFC 3977 (8.4) names the size fields ":bytes" and ":lines";
		// older servers, and our own format, "Bytes:" and "Lines:".
		if (strcasecmp(field.c_str(), ":bytes") == 0)
			field = "Bytes:";
		else if (strcasecmp(field.c_str(), ":lines") == 0)
			field = "Lines:";
		for (i = 0; i < _osz; i++) {
			if (field.length() >= (unsigned int) _over[i].len
			    && strncasecmp(field.data(), _over[i].name,
					   _over[i].len) == 0) {
				if (field.length() ==
				    (unsigned int) _over[i].len + 4
				    && strncasecmp("full",
						   field.data() +
						   _over[i].len, 4) == 0) {
					_trans[i].full = 1;
				} else {
					_trans[i].full = 0;
				}
				_trans[i].trans = fn;
			}
		}
		fn++;
	}
	dotrans = 0;
	if (_osz != fn) {
		dotrans = 1;
	} else {
		for (i = 0; i < _osz; i++) {
			if (_trans[i].trans != i
			    || _trans[i].full != _over[i].full) {
				dotrans = 1;
				break;
			}
		}
	}
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
