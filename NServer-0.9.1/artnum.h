#ifndef _artnum_h_
#define _artnum_h_

#include <ctype.h>
#include <errno.h>
#include <stdint.h>
#include <stdlib.h>

/*
 * Article numbers. RFC 3977 allows 1 to 2^31-1, but the big binary
 * providers are far past that (alt.binaries.* groups above 6*10^10), so
 * NewsCache keeps them in 64 bits everywhere. 0 is not an article number;
 * it stands for "none".
 */
typedef uint64_t artnum_t;
#define ARTNUM_MAX UINT64_MAX

/*
 * Reads the decimal article number at s. Returns 1 and stores it in *n
 * (and the end of the digits in *end, if end isn't NULL), or returns 0 if
 * s doesn't start with a digit or the number doesn't fit in artnum_t.
 */
inline int parse_artnum(const char *s, artnum_t *n, const char **end = NULL)
{
	char *e;
	unsigned long long v;

	if (!isdigit((unsigned char) *s))
		return 0;
	errno = 0;
	v = strtoull(s, &e, 10);
	if (errno == ERANGE || v > ARTNUM_MAX)
		return 0;
	*n = (artnum_t) v;
	if (end)
		*end = e;
	return 1;
}

/*
 * Article number at the start of s (an overview line, a LISTGROUP line),
 * or 0 if there is none.
 */
inline artnum_t leading_artnum(const char *s)
{
	artnum_t n;
	return parse_artnum(s, &n) ? n : 0;
}

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
