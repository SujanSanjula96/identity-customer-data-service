#!/usr/bin/env python3
"""Make the test data of the CDS performance tests (T2 to T8).

All rule attributes are traits, so that the data does not depend on the IS
claims. The values are deterministic: the same seed number gives the same row.

Subcommands:
    data-gen.py seed <count> <out.csv>
        The existing profiles. 70 % are permanent profiles (with a user ID and
        rule attributes). 30 % are anonymous profiles (no rule attributes).
        Columns: index,kind,user_id,email,phone,nic,first_name,last_name,address,alt_email,alt_name

    data-gen.py incoming <seed count> <offset> <rows> <out.csv> [<exact %> <fuzzy %>]
        Profiles that come in during a test (T5, T6, T7). The default mix is
        20 % exact match, 20 % fuzzy match only, and 60 % no match. An exact row
        copies all rule values of one permanent seed profile. A fuzzy row has new
        email, phone, and nic values, a typo in the first name and in the
        address, and the same last name. Use a new offset for each test level,
        so that each level has new values.
        Columns: kind,email,phone,nic,first_name,last_name,address

The matcher of CDS ignores some values (for example the domains example.com and
test.com, and local parts such as test or admin). The values here avoid them.
"""

import csv
import random
import sys

STREETS = ("galle marine temple lotus flower park station lake hill "
           "church school market river garden palm cotton jasmine rose orchid maple cedar "
           "willow oak birch elm pine chestnut highland meadow sunset harbor bridge mill "
           "castle king queen prince victoria albert union liberty canal forest valley "
           "spring summer autumn winter north south east west central").split()
CITIES = ("colombo kandy galle negombo jaffna matara kurunegala anuradhapura batticaloa "
          "trincomalee ratnapura badulla").split()
DOMAINS = ("gmail.com yahoo.com outlook.com hotmail.com icloud.com proton.me "
           "zoho.com fastmail.com").split()


# Names. CDS makes one Double Metaphone code (4 characters at most) for each word
# of a name, and gives two names with the same codes a score of 0.9 (above the
# review threshold). A list of real names has few codes, so a fuzzy name rule
# finds many candidates for each profile, and a "no match" row is a review
# task. Thus each person gets a made-up name of two words. Each word has 4
# consonants with different codes (11 ** 4 = 14641 codes for each word), and
# the two words take their consonants from different parts of the person
# number. Two persons below 214 million never have the same codes on both words.
CONSONANTS = ("p", "t", "k", "f", "s", "m", "n", "l", "r", "sh", "j")
VOWELS = ("a", "e", "i", "o", "u")
WORD_CODES = len(CONSONANTS) ** 4
# The person numbers of the "no match" rows start here, after all seed numbers.
NEW_PERSON_BASE = 2000000


def word(code, salt):
    """A made-up word: 4 consonants from code (0 to WORD_CODES - 1), vowels from salt."""
    letters = []
    for _ in range(4):
        letters.append(CONSONANTS[code % len(CONSONANTS)])
        code //= len(CONSONANTS)
        letters.append(VOWELS[salt % len(VOWELS)])
        salt //= len(VOWELS)
    return "".join(letters)


def two_words(p, k):
    """Two words for person p. k selects a different name (first or last) of the person."""
    q = p * 4 + k
    code1 = (q * 7919) % WORD_CODES
    code2 = (q // WORD_CODES + q * 3 + 5003) % WORD_CODES
    salt = (q * 2654435761) >> 7
    return f"{word(code1, salt)} {word(code2, salt >> 9)}"


def name_parts(i):
    return two_words(i, 0), two_words(i, 1)


def local_part(first, last):
    return f"{first.split()[0]}.{last.split()[0]}"


def seed_row(i):
    """The values of seed profile i. Kind: permanent or anonymous."""
    kind = "anonymous" if i % 10 >= 7 else "permanent"
    first, last = name_parts(i)
    email = f"{local_part(first, last)}.{i}@{DOMAINS[i % len(DOMAINS)]}"
    phone = f"+9477{1000000 + (i * 7919) % 9000000:07d}"
    nic = f"{199000000000 + i * 37:012d}"
    # 4 % of the permanent profiles share the email, the phone, and the nic with
    # the profile 1 to 4 places before them (groups of 2 to 5). 2 small groups of
    # about 50 profiles share one "hot" email. The rules see more candidates for
    # these values.
    if i % 100 in (1, 2, 3, 4) and i >= 100:
        base = i - i % 100
        if seed_kind(base) == "permanent":
            bf, bl = name_parts(base)
            email = f"{local_part(bf, bl)}.{base}@{DOMAINS[base % len(DOMAINS)]}"
            phone = f"+9477{1000000 + (base * 7919) % 9000000:07d}"
            nic = f"{199000000000 + base * 37:012d}"
    if i % 1000 == 5:
        email = f"family.{(i // 1000) % 2}@{DOMAINS[0]}"
    # The house number makes each seed address different.
    address = f"{i + 1} {STREETS[i % len(STREETS)]} road {CITIES[(i // len(STREETS)) % len(CITIES)]}"
    return {
        "index": i, "kind": kind, "user_id": f"seed-user-{i}" if kind == "permanent" else "",
        "email": email, "phone": phone, "nic": nic, "first_name": first, "last_name": last,
        "address": address, "alt_email": email, "alt_name": f"{first} {last}",
    }


def seed_kind(i):
    return "anonymous" if i % 10 >= 7 else "permanent"


def typo(value, rnd):
    """One small change that a fuzzy rule must still match: swap two inner letters."""
    letters = [p for p in range(1, len(value) - 2) if value[p].isalpha() and value[p + 1].isalpha()]
    if not letters:
        return value
    p = rnd.choice(letters)
    if value[p] == value[p + 1]:
        return value[:p] + ("a" if value[p] != "a" else "e") + value[p + 1:]
    return value[:p] + value[p + 1] + value[p] + value[p + 2:]


def new_values(n, rnd):
    person = NEW_PERSON_BASE + n
    first, last = two_words(person, 0), two_words(person, 1)
    return {
        "email": f"{local_part(first, last)}{n}.new@{rnd.choice(DOMAINS)}",
        # perf-test-cds.sh gives each level the offset level * 10,000,000. The
        # digit after +947 comes from the level, so that two levels never make the
        # same phone (an exact phone rule would merge them). It is never 7, the
        # prefix of the seed phones (+9477).
        "phone": f"+947{(n // 10000000) % 7}{n % 10000000:07d}",
        "nic": f"{200500000000 + n:012d}",
        "first_name": first, "last_name": last,
        "address": f"{rnd.randint(1, 900)} {rnd.choice(STREETS)} lane {rnd.choice(CITIES)}",
    }


def cmd_seed(count, out):
    cols = ["index", "kind", "user_id", "email", "phone", "nic", "first_name", "last_name",
            "address", "alt_email", "alt_name"]
    with open(out, "w", newline="") as handle:
        w = csv.DictWriter(handle, fieldnames=cols)
        w.writeheader()
        for i in range(count):
            w.writerow(seed_row(i))
    return 0


def cmd_incoming(seed_count, offset, rows, out, exact_pct=20, fuzzy_pct=20):
    rnd = random.Random(offset)
    permanent = [i for i in range(min(seed_count, 1000000)) if seed_kind(i) == "permanent"]
    if not permanent:
        print("The seed has no permanent profiles.", file=sys.stderr)
        return 1
    cols = ["kind", "email", "phone", "nic", "first_name", "last_name", "address"]
    with open(out, "w", newline="") as handle:
        w = csv.writer(handle)
        w.writerow(cols)
        for j in range(rows):
            n = offset + j
            pick = rnd.random() * 100
            if pick < exact_pct:
                s = seed_row(rnd.choice(permanent))
                w.writerow(["exact", s["email"], s["phone"], s["nic"], s["first_name"],
                            s["last_name"], s["address"]])
            elif pick < exact_pct + fuzzy_pct:
                s = seed_row(rnd.choice(permanent))
                v = new_values(n, rnd)
                w.writerow(["fuzzy", v["email"], v["phone"], v["nic"], typo(s["first_name"], rnd),
                            s["last_name"], typo(s["address"], rnd)])
            else:
                v = new_values(n, rnd)
                w.writerow(["none", v["email"], v["phone"], v["nic"], v["first_name"],
                            v["last_name"], v["address"]])
    return 0


def main(argv):
    if len(argv) >= 3 and argv[0] == "seed":
        return cmd_seed(int(argv[1]), argv[2])
    if len(argv) in (5, 7) and argv[0] == "incoming":
        extra = [float(x) for x in argv[5:7]]
        return cmd_incoming(int(argv[1]), int(argv[2]), int(argv[3]), argv[4], *extra)
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
