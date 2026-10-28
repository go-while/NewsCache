// Checks md5lib against the test suite of RFC 1321 (A.5) and md5crypt (the
// auth file's 24-character form, base64 of the MD5 digest). On big-endian
// hosts the MD5 code didn't reverse its words (HIGHFIRST was never defined),
// so every digest was wrong there and MD5 passwords never matched; configure
// now detects the byte order. On little-endian hosts this guards the same
// code path.

#include "config.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef MD5_CRYPT
#include "../../md5lib/md5.h"
#include "../../md5lib/md5crypt.h"

static int failures = 0;

static void check_digest(const char *in, const char *want)
{
	struct MD5Context ctx;
	unsigned char d[16];
	char hex[33];

	MD5Init(&ctx);
	MD5Update(&ctx, (unsigned char const *) in, strlen(in));
	MD5Final(d, &ctx);
	for (int i = 0; i < 16; i++)
		snprintf(hex + 2 * i, 3, "%02x", d[i]);
	if (strcmp(hex, want) != 0) {
		printf("FAIL MD5(\"%s\") = %s, want %s\n", in, hex, want);
		failures++;
	} else {
		printf("ok   MD5(\"%.20s%s\")\n", in, strlen(in) > 20 ? "..." : "");
	}
}

int main()
{
#ifdef WORDS_BIGENDIAN
	printf("big-endian host\n");
#else
	printf("little-endian host\n");
#endif
	// RFC 1321, A.5
	check_digest("", "d41d8cd98f00b204e9800998ecf8427e");
	check_digest("a", "0cc175b9c0f1b6a831c399e269772661");
	check_digest("abc", "900150983cd24fb0d6963f7d28e17f72");
	check_digest("message digest", "f96b697d7cb7938d525a2f31aaf161d0");
	check_digest("abcdefghijklmnopqrstuvwxyz",
		     "c3fcd3d76192e4007dfb496cca67e13b");
	check_digest("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
		     "d174ab98d277d9f5a5611c2c9f419d9f");
	check_digest("1234567890123456789012345678901234567890"
		     "1234567890123456789012345678901234567890",
		     "57edf4a22be3c955ac49da2e2107b67a");

	// the auth file's form: base64 of the digest ("secret")
	char *h = md5crypt("secret");
	if (!h || strcmp(h, "Xr4ilOzQ4PCOq3aQ0qbuaQ==") != 0) {
		printf("FAIL md5crypt(\"secret\") = %s, want Xr4ilOzQ4PCOq3aQ0qbuaQ==\n",
		       h ? h : "(null)");
		failures++;
	} else {
		printf("ok   md5crypt(\"secret\")\n");
	}
	free(h);

	return failures ? 1 : 0;
}
#else
int main()
{
	printf("built without MD5 (--without-md5): skipped\n");
	return 77;
}
#endif
