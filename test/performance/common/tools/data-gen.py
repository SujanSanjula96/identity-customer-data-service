#!/usr/bin/env python3
"""Make the test data of the CDS performance tests (T2 to T8).

All rule attributes are traits, so that the data does not depend on the IS
claims. The values are deterministic: the same seed number gives the same row.

The values are realistic for a customer base in Sri Lanka (the phone and NIC
formats), with names from all communities and some international names:
  - Names repeat as in a real population. A few first names and surnames are
    very common (for example Perera and Fernando), and there is a long tail of
    rare names, made from real name parts. So the exact name keys and the fuzzy
    name keys of CDS (phonetic codes and LSH bands of character bigrams) have
    groups of a realistic size: some common values match more than 100 profiles,
    and CDS skips them, as it does with real data.
  - Addresses have a house number, a street, a street type, and a city, with
    more people in the large cities.
  - Emails follow common patterns of the name, phones use the formats that users
    type, and NICs use the old and the new format. These values are unique for
    each person, except the planned shared groups below.

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
        address, and the same last name. A "no match" row is a new person. Its
        name can still be a common name, as with real data. Use a new offset for
        each test level, so that each level has new values.
        Columns: kind,email,phone,nic,first_name,last_name,address

    data-gen.py stats <out.csv>
        Print how often the most common names, cities, and email domains occur.

The matcher of CDS ignores some values (for example the domains example.com and
test.com, and local parts such as test or admin). The values here avoid them.
"""

import bisect
import csv
import itertools
import random
import sys
from collections import Counter

# --------------------------------------------------------------------------- #
# Name lists. The order is the rank: a name earlier in a list is more common.
# --------------------------------------------------------------------------- #
FIRST_NAMES = """
Nimal Kamal Sunil Saman Chaminda Mohamed Nuwan Ruwan Kasun Tharindu
Dilshan Chathura Pradeep Mahesh Lahiru Sampath Asanka Dinesh Prasad Ajith
Chamari Nadeesha Dilani Sanduni Kumari Nirosha Dilrukshi Ishara Thilini Malini
Kavindi Sachini Hiruni Dulani Anusha Shalini Priyanka Gayani Nilmini Rasika
Fathima Ahamed Mohamed Rizwan Fazil Imran Nazeer Rifkhan Ayesha Zainab
Suresh Ramesh Sivakumar Kumar Murali Ganesh Rajesh Vijay Arun Karthik
Priya Lakshmi Kavitha Meena Deepa Sangeetha Nirmala Vasuki Thanuja Malar
Isuru Shehan Sahan Janith Ravindu Pasindu Yasiru Hasitha Sanjaya Chamara
Upul Lasantha Rohan Anura Gamini Jagath Keerthi Lalith Mahinda Upali
Sandya Chandrika Anoma Kanthi Shiromi Damayanthi Pushpa Indrani Mallika Swarna
Dimuthu Supun Ishan Nipun Charith Kalana Udara Dulanjana Akila Sameera
Hansika Piumi Sewwandi Imesha Nethmi Tharushi Senuri Methmi Dinithi Chamodi
Peter John David Michael Daniel Andrew Mark Paul James Thomas
Mary Anne Sarah Maria Christine Michelle Rebecca Natasha Shirani Rochelle
Aravinda Mahela Kumar Rangana Angelo Kusal Dasun Wanindu Charitha Lasith
Chanaka Dhanushka Hemantha Indika Janaka Kapila Lakmal Manjula Nalin Prabath
Roshan Sajith Thushara Waruna Asela Buddhika Chandana Duminda Eranga Gihan
Harsha Iresha Jeewan Kanishka Lakshan Madushan Nalaka Oshada Pathum Rajitha
Sudesh Tharaka Upeksha Vimukthi Wasantha Yohan Achini Buddhini Chathurika Dinusha
Erandi Geethika Harshani Inoka Jayani Kalpani Lasanthi Madhavi Nayomi Oshadi
Pavithra Rangani Samanthi Tharanga Udeshika Vindya Wathsala Yashodha Amaya Bhagya
Abdul Hassan Hussain Ibrahim Ismail Jamal Kareem Mansoor Naushad Riyas
Safna Shiffa Fazeena Hameeda Jasmin Nusra Raheema Sameera Shafna Zahira
Thevarasa Selvam Balachandran Gnanam Jeyakumar Kanagaraj Mahendran Nadarajah Paramesh Sathish
Abirami Bhavani Gowri Janani Keerthana Mathivathani Nithya Pavithra Revathi Sharmila
""".split()

LAST_NAMES = """
Perera Fernando Silva de_Silva Jayasinghe Bandara Wickramasinghe Dissanayake Kumara Herath
Rathnayake Gunawardena Senanayake Weerasinghe Ranasinghe Karunaratne Wijesinghe Liyanage Jayawardena Rajapaksa
Mendis Peiris Gunasekara Jayasuriya Wijeratne Samarasinghe Amarasinghe Abeysekara Ekanayake Gamage
Kumarasinghe Wanigasekara Siriwardena Pathirana Rodrigo Cooray Gomes Dias Costa Fonseka
Mohamed Ahamed Fernandopulle Marikar Cassim Rahman Hussain Haniffa Ismail Jaffer
Sivakumar Rajendran Selvarajah Thambiah Kanagaratnam Sivanathan Balasubramaniam Navaratnam Ratnam Arumugam
Muthukumarana Hettiarachchi Ranatunga Karunanayake Wijetunga Abeywardena Gunathilake Hewage Kodituwakku Madushanka
Nanayakkara Obeyesekere Premadasa Rupasinghe Tennakoon Udugama Vithanage Wanniarachchi Yapa Zoysa
Atapattu Bopage Chandrasiri Dayaratne Edirisinghe Goonetilleke Illangakoon Jayakody Kulatunga Lokuge
Munasinghe Nawarathna Pushpakumara Ratnayake Sumanasekara Thilakaratne Wickremaratne Withana Yatawara Dahanayake
Smith Brown Johnson Williams Taylor Anderson Thomas Jackson White Martin
Kariyawasam Kodagoda Lakmal Malalasekera Panditharatne Ranawaka Seneviratne Thenuwara Upasena Weerakoon
Nadarajah Pillai Shanmugam Subramaniam Thurairajah Vigneswaran Yogarajah Ganeshan Kandiah Murugesu
Ansar Bakeer Faleel Hameed Ibrahim Latheef Mansoor Nizar Rizvi Thahir
Alwis Bastians Collette Daniel Ephraims Fernandez Ludowyk Ondaatje Pereira VanDort
""".split()
LAST_NAMES = [n.replace("_", " ") for n in LAST_NAMES]
# Real name parts for the long tail of rare names.
FIRST_PREFIX = ("Chath Dil Kas Nim San Tha Har Pras Ish Lah Ruw Sas Kav Nad Ses Hiru Dula Shen Yas Mal "
                "Thil Vin Anu Bim Dhan Ama Sur Jan Ran Nish Sanj Prab Mad Dar Lak Uda").split()
FIRST_SUFFIX = ("ura an un esh ini ika ara ith eepa antha ushka ama ani eetha uni ena idu ath anga").split()
LAST_PREFIX = ("Wickrama Jaya Guna Rana Weera Siri Abey Karuna Samara Amara Wije Ratna Dharma Seneh Sumana "
               "Dissa Ekana Liya Herath Kula Muna Pathi Hetti Nawa Kodi Thila Wanni Ilanga Edi Pushpa").split()
LAST_SUFFIX = ("singhe wardena ratne sekara nayake suriya tunga koon arachchi wasam pala siri bandara "
               "kumara rathna deniya gama tilleke").split()
FIRST_MIDDLE = ("", "", "a", "i", "u", "ee", "an", "ith", "ar", "es")
LAST_MIDDLE = ("", "", "ra", "na", "wa", "tha", "sa", "ma", "la")

STREETS = """
Galle Temple Station Lake Church School Market Hospital Main Hill Park Flower Lotus Marine Sea
Kandy High Old New Dharmapala Ananda Mahinda Buddhaloka Siripala Sri_Sangaraja Bauddhaloka Baseline
Havelock Duplication Nawala Pagoda Kirula Thimbirigasyaya Jawatta Ward Rosmead Horton Gregory
Pamankada Vihara Dewala Pansala Kovil Mosque Railway Canal Garden Cemetery Factory Mill Bridge
Peradeniya Katugastota Kurunegala Colombo Negombo Matara Ratnapura Badulla Jaffna Hospital_Junction
""".split()
STREETS = [s.replace("_", " ") for s in STREETS]
STREET_TYPES = (("Road", 45), ("Mawatha", 20), ("Lane", 15), ("Place", 8), ("Avenue", 6),
                ("Street", 6))
CITIES = """
Colombo Dehiwala Moratuwa Maharagama Kotte Kandy Negombo Kaduwela Kelaniya Panadura
Galle Jaffna Kurunegala Gampaha Kalutara Matara Batticaloa Ratnapura Badulla Anuradhapura
Trincomalee Kegalle Polonnaruwa Hambantota Nuwara_Eliya Matale Chilaw Puttalam Vavuniya Ampara
Homagama Piliyandala Kesbewa Wattala Ja-Ela Kadawatha Kiribathgoda Nugegoda Battaramulla Malabe
Embilipitiya Horana Avissawella Kuliyapitiya Dambulla Bandarawela Hatton Tangalle Weligama Beruwala
""".split()
CITIES = [c.replace("_", " ") for c in CITIES]
# Email domains and how often they occur.
DOMAINS = (("gmail.com", 62), ("yahoo.com", 12), ("hotmail.com", 8), ("outlook.com", 6),
           ("icloud.com", 4), ("sltnet.lk", 3), ("proton.me", 2), ("live.com", 2), ("zoho.com", 1))
# Mobile prefixes in Sri Lanka (operator codes) and how often they occur.
MOBILE_PREFIXES = (("77", 30), ("71", 22), ("76", 18), ("70", 10), ("72", 8), ("75", 7), ("78", 5))
# Keys on a QWERTY keyboard and their neighbours, for realistic typos.
NEIGHBOURS = {
    "a": "qsz", "b": "vgn", "c": "xdv", "d": "sfe", "e": "wrd", "f": "dgr", "g": "fht", "h": "gjy",
    "i": "uok", "j": "hku", "k": "jli", "l": "kop", "m": "nj", "n": "bmh", "o": "ipl", "p": "ol",
    "q": "wa", "r": "eft", "s": "adw", "t": "rgy", "u": "yij", "v": "cbf", "w": "qes", "x": "zcs",
    "y": "thu", "z": "xa",
}
# Share of persons who get a rare name (from the name parts) instead of a listed name.
RARE_FIRST = 0.40
RARE_LAST = 0.35
# The person numbers of the "no match" rows start here, after all seed numbers.
NEW_PERSON_BASE = 2000000


def zipf_table(items, exponent):
    """Cumulative weights for a Zipf-like distribution over the ranked items."""
    weights = [1.0 / (rank ** exponent) for rank in range(1, len(items) + 1)]
    return list(itertools.accumulate(weights))


def weighted_table(pairs):
    return [p[0] for p in pairs], list(itertools.accumulate(p[1] for p in pairs))


# The exponents give a realistic concentration: the most common first name is
# about 1.5 % of the persons, and the most common surname (Perera) about 5 %.
FIRST_CUM = zipf_table(FIRST_NAMES, 0.45)
LAST_CUM = zipf_table(LAST_NAMES, 0.75)
CITY_CUM = zipf_table(CITIES, 0.8)
STREET_CUM = zipf_table(STREETS, 0.6)
STREET_TYPE_VALUES, STREET_TYPE_CUM = weighted_table(STREET_TYPES)
DOMAIN_VALUES, DOMAIN_CUM = weighted_table(DOMAINS)
MOBILE_VALUES, MOBILE_CUM = weighted_table(MOBILE_PREFIXES)


def pick(rnd, values, cum):
    return values[bisect.bisect_left(cum, rnd.random() * cum[-1])]


def person_rnd(p, what):
    """A random source that depends only on the person number and the purpose."""
    return random.Random(f"{what}:{p}")


def join_parts(*parts):
    """Join name parts. Two vowels at a join become one, so that a name has no
    run of three or more vowels (for example "San" + "ee" + "eetha")."""
    name = ""
    for part in parts:
        if name and part and name[-1] in "aeiou" and part[0] in "aeiou":
            part = part.lstrip("aeiou") or part[-1]
        name += part
    return name


def first_name(p):
    rnd = person_rnd(p, "first")
    if rnd.random() < RARE_FIRST:
        return join_parts(rnd.choice(FIRST_PREFIX), rnd.choice(FIRST_MIDDLE), rnd.choice(FIRST_SUFFIX))
    return pick(rnd, FIRST_NAMES, FIRST_CUM)


def last_name(p):
    rnd = person_rnd(p, "last")
    if rnd.random() < RARE_LAST:
        return join_parts(rnd.choice(LAST_PREFIX), rnd.choice(LAST_MIDDLE), rnd.choice(LAST_SUFFIX))
    return pick(rnd, LAST_NAMES, LAST_CUM)


def address(p, rnd=None):
    rnd = rnd or person_rnd(p, "address")
    number = rnd.randint(1, 400)
    if rnd.random() < 0.15:
        number = f"{number}/{rnd.randint(1, 12)}"
    elif rnd.random() < 0.08:
        number = f"{number}{rnd.choice('ABC')}"
    street = f"{pick(rnd, STREETS, STREET_CUM)} {pick(rnd, STREET_TYPE_VALUES, STREET_TYPE_CUM)}"
    return f"{number}, {street}, {pick(rnd, CITIES, CITY_CUM)}"


def email(p, first, last, unique):
    """An email in a common pattern of the name. unique makes it unique for each person."""
    rnd = person_rnd(p, "email")
    f, l = first.lower(), last.lower().replace(" ", "")
    pattern = rnd.randrange(6)
    local = (f"{f}.{l}", f"{f}{l}", f"{f[0]}{l}", f"{f}_{l}", f"{l}.{f}", f"{f}.{l[0]}")[pattern]
    # Many real addresses carry a number (a year or a short number). Here all do,
    # because two persons with the same name must not share an address (an exact
    # email rule would merge them).
    local = f"{local}{unique}"
    return f"{local}@{pick(rnd, DOMAIN_VALUES, DOMAIN_CUM)}"


def phone(prefix, number, style):
    """A mobile number in one of the formats that users type."""
    digits = f"{number:07d}"
    return (f"+94{prefix}{digits}", f"0{prefix}{digits}", f"+94 {prefix} {digits[:3]} {digits[3:]}",
            f"0{prefix}-{digits}", f"94{prefix}{digits}")[style]


def seed_kind(i):
    return "anonymous" if i % 10 >= 7 else "permanent"


def seed_person(i):
    """The rule values of seed person i, before the shared groups."""
    first, last = first_name(i), last_name(i)
    rnd = person_rnd(i, "contact")
    mobile = pick(rnd, MOBILE_VALUES, MOBILE_CUM)
    return {
        "first_name": first, "last_name": last,
        "email": email(i, first, last, i),
        # The seed numbers are unique: 7 digits from the person number.
        "phone": phone(mobile, (i * 7919) % 10000000, rnd.randrange(5)),
        # The NIC must be unique. The index makes it unique; nic() gives the format.
        "nic": unique_nic(i),
        "address": address(i),
    }


def unique_nic(i):
    """A unique NIC for seed person i, in the old or the new format."""
    rnd = person_rnd(i, "nic")
    if rnd.random() < 0.45:
        # Old format: 9 digits and V. The digits come from the person number.
        return f"{(100000000 + i * 37) % 1000000000:09d}V"
    return f"{199000000000 + i * 37:012d}"


def seed_row(i):
    """The values of seed profile i. Kind: permanent or anonymous."""
    kind = seed_kind(i)
    v = seed_person(i)
    # 4 % of the permanent profiles share the email, the phone, and the nic with
    # the profile 1 to 4 places before them (groups of 2 to 5): the same person
    # with more than one profile. 2 small groups of about 50 profiles share one
    # "hot" email (a family or a shop address). The rules see more candidates
    # for these values.
    if i % 100 in (1, 2, 3, 4) and i >= 100:
        base = i - i % 100
        if seed_kind(base) == "permanent":
            b = seed_person(base)
            v["email"], v["phone"], v["nic"] = b["email"], b["phone"], b["nic"]
    if i % 1000 == 5:
        v["email"] = f"family.{(i // 1000) % 2}@gmail.com"
    return {
        "index": i, "kind": kind, "user_id": f"seed-user-{i}" if kind == "permanent" else "",
        "email": v["email"], "phone": v["phone"], "nic": v["nic"], "first_name": v["first_name"],
        "last_name": v["last_name"], "address": v["address"], "alt_email": v["email"],
        "alt_name": f"{v['first_name']} {v['last_name']}",
    }


def typo(value, rnd):
    """One realistic typo that a fuzzy rule must still match: a swap, a dropped
    letter, a doubled letter, or a neighbour key on the keyboard."""
    letters = [p for p in range(1, len(value) - 1) if value[p].isalpha()]
    if not letters:
        return value
    p = rnd.choice(letters)
    kind = rnd.randrange(4)
    if kind == 0 and value[p + 1].isalpha() and value[p] != value[p + 1]:
        return value[:p] + value[p + 1] + value[p] + value[p + 2:]
    if kind == 1:
        return value[:p] + value[p + 1:]
    if kind == 2:
        return value[:p] + value[p] + value[p:]
    near = NEIGHBOURS.get(value[p].lower())
    if not near:
        return value[:p] + value[p + 1:]
    c = rnd.choice(near)
    return value[:p] + (c.upper() if value[p].isupper() else c) + value[p + 1:]


def new_values(n, rnd):
    """A new person for an incoming row. n is the row number (offset + row)."""
    person = NEW_PERSON_BASE + n
    first, last = first_name(person), last_name(person)
    return {
        "email": email(person, first, last, f"{n}n"),
        # perf-test-cds.sh gives each level the offset level * 10,000,000. The
        # second digit of the operator code comes from the level, so that two
        # levels never make the same phone (an exact phone rule would merge them).
        # The operator code starts with 6 (not a mobile code), so a new phone never
        # matches a seed phone.
        "phone": phone(f"6{(n // 10000000) % 10}", n % 10000000, rnd.randrange(5)),
        "nic": f"{200500000000 + n:012d}",
        "first_name": first, "last_name": last,
        "address": address(person, rnd),
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
            choice = rnd.random() * 100
            if choice < exact_pct:
                s = seed_row(rnd.choice(permanent))
                w.writerow(["exact", s["email"], s["phone"], s["nic"], s["first_name"],
                            s["last_name"], s["address"]])
            elif choice < exact_pct + fuzzy_pct:
                s = seed_row(rnd.choice(permanent))
                v = new_values(n, rnd)
                w.writerow(["fuzzy", v["email"], v["phone"], v["nic"], typo(s["first_name"], rnd),
                            s["last_name"], typo(s["address"], rnd)])
            else:
                v = new_values(n, rnd)
                w.writerow(["none", v["email"], v["phone"], v["nic"], v["first_name"],
                            v["last_name"], v["address"]])
    return 0


def cmd_stats(path):
    rows = [r for r in csv.DictReader(open(path)) if r.get("kind", "") != "anonymous"]
    total = len(rows)
    print(f"rows with rule values: {total}")
    for col, label in (("first_name", "first names"), ("last_name", "surnames")):
        c = Counter(r[col] for r in rows)
        once = sum(1 for n in c.values() if n == 1)
        top = ", ".join(f"{k} {100 * n / total:.1f} %" for k, n in c.most_common(5))
        print(f"{label}: {len(c)} distinct, {once} used once. Top: {top}")
    c = Counter(r["address"].rsplit(", ", 1)[-1] for r in rows)
    print("cities: " + ", ".join(f"{k} {100 * n / total:.1f} %" for k, n in c.most_common(5)))
    c = Counter(r["email"].split("@")[-1] for r in rows)
    print("domains: " + ", ".join(f"{k} {100 * n / total:.1f} %" for k, n in c.most_common(5)))
    for col in ("email", "phone", "nic"):
        c = Counter(r[col] for r in rows)
        print(f"{col}: {len(c)} distinct of {total}")
    return 0


def main(argv):
    if len(argv) >= 3 and argv[0] == "seed":
        return cmd_seed(int(argv[1]), argv[2])
    if len(argv) in (5, 7) and argv[0] == "incoming":
        extra = [float(x) for x in argv[5:7]]
        return cmd_incoming(int(argv[1]), int(argv[2]), int(argv[3]), argv[4], *extra)
    if len(argv) == 2 and argv[0] == "stats":
        return cmd_stats(argv[1])
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
