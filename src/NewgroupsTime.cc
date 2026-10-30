#include "config.h"
#include "NewgroupsTime.h"

#include <errno.h>

bool newgroups_time(struct tm value, bool gmt, time_t *result)
{
	errno = 0;
	time_t converted;
	if (gmt) {
		converted = timegm(&value);
	} else {
		value.tm_isdst = -1;
		converted = mktime(&value);
	}
	// -1 can be a valid pre-epoch instant, not just an error.
	if (converted == (time_t) -1 && errno != 0)
		return false;
	*result = converted < 0 ? 0 : converted;
	return true;
}
