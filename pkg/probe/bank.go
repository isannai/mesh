package probe

// bank.go: the question bank written in code. Four categories whose questions are a
// fixed table of fact pairs:
//
//	verb      the past tense of an irregular verb              swim -> swam
//	siunit    the SI unit of a quantity                        force -> newton
//	isocode   the ISO 4217 code of a currency                  Japanese yen -> JPY
//	domain    the internet country code of a country           Japan -> .jp
//
// WHY A TABLE AND NOT A QUESTION WRITER. The other knowledge categories (geography,
// element, currency) are written by a model, so their answers are drafts that can be
// wrong. These four are facts with one defensible answer each, short enough to write
// down, and a wrong answer in the table would fail honest nodes. So they are checked
// by hand here and need no writer node: they keep working when every writer is
// unreachable, like the arithmetic.
//
// WHAT A TABLE GIVES UP. Arithmetic is unbounded; a table is not. A node that has
// stored the whole table can answer without a model, which the quiz cannot rule out
// anyway (it says nothing about which model a node runs: that is the reference
// check's job). Each table has dozens of entries, so a guesser gets nothing, and a
// question is not asked again for as long as the history is kept.
//
// WHAT GOES IN. The same rules as the model-written categories (question.go):
//
//   - one defensible answer: "dream" is left out (dreamed or dreamt), "length" is left
//     out of the units (metre or meter), a verb whose past is its own base (cut) is
//     left out because the question answers itself.
//   - not a word of ordinary English as the answer, because answers are scored as word
//     sets: a reply that begins "It is" would match the domain ".it" with no knowledge.
//     So Italy (.it), Iceland (.is), India (.in), Norway (.no), Belgium (.be), Austria
//     (.at) and Malaysia (.my) are not asked.
//   - the two entries each category shows as its examples are not in its table, so an
//     example is never also a question (parseBatch does the same for written ones).

import (
	"fmt"
	"math/rand"
)

// fact is one row of a bank: what is asked about, and the answer.
type fact struct{ subject, answer string }

// bank is one table and how it is asked.
type bank struct {
	cat     Category
	ask     string // the question, with the subject at %s
	facts   []fact
	fewshot []QA // two examples of the same kind, never in facts
}

func (b bank) question(rng *rand.Rand) Question {
	f := b.facts[rng.Intn(len(b.facts))]
	return Question{
		Category: b.cat,
		Q:        fmt.Sprintf(b.ask, f.subject),
		Draft:    f.answer,
		Fewshot:  b.fewshot,
	}
}

var (
	verbBank = bank{
		cat: CatVerb,
		ask: `What is the past tense of "%s"?`,
		fewshot: []QA{
			{Q: `What is the past tense of "go"?`, A: "went"},
			{Q: `What is the past tense of "eat"?`, A: "ate"},
		},
		facts: []fact{
			{"swim", "swam"}, {"see", "saw"}, {"take", "took"}, {"give", "gave"}, {"begin", "began"},
			{"drink", "drank"}, {"sing", "sang"}, {"ring", "rang"}, {"run", "ran"}, {"come", "came"},
			{"write", "wrote"}, {"drive", "drove"}, {"speak", "spoke"}, {"break", "broke"}, {"choose", "chose"},
			{"freeze", "froze"}, {"steal", "stole"}, {"fly", "flew"}, {"grow", "grew"}, {"know", "knew"},
			{"throw", "threw"}, {"draw", "drew"}, {"blow", "blew"}, {"wear", "wore"}, {"tear", "tore"},
			{"swear", "swore"}, {"hide", "hid"}, {"ride", "rode"}, {"rise", "rose"}, {"fall", "fell"},
			{"forget", "forgot"}, {"get", "got"}, {"sit", "sat"}, {"stand", "stood"}, {"understand", "understood"},
			{"find", "found"}, {"bind", "bound"}, {"grind", "ground"}, {"buy", "bought"}, {"bring", "brought"},
			{"think", "thought"}, {"catch", "caught"}, {"teach", "taught"}, {"fight", "fought"}, {"seek", "sought"},
			{"sell", "sold"}, {"tell", "told"}, {"hold", "held"}, {"keep", "kept"}, {"sleep", "slept"},
			{"sweep", "swept"}, {"leave", "left"}, {"feel", "felt"}, {"meet", "met"}, {"lead", "led"},
			{"feed", "fed"}, {"make", "made"}, {"pay", "paid"}, {"say", "said"}, {"lay", "laid"},
			{"send", "sent"}, {"spend", "spent"}, {"build", "built"}, {"lend", "lent"}, {"bend", "bent"},
			{"lose", "lost"}, {"win", "won"}, {"shoot", "shot"}, {"dig", "dug"}, {"stick", "stuck"},
			{"strike", "struck"}, {"spin", "spun"}, {"swing", "swung"}, {"sting", "stung"}, {"cling", "clung"},
			{"sink", "sank"}, {"hear", "heard"}, {"mean", "meant"}, {"deal", "dealt"}, {"slide", "slid"},
			{"bite", "bit"}, {"shake", "shook"}, {"forgive", "forgave"}, {"mistake", "mistook"}, {"withdraw", "withdrew"},
			{"become", "became"}, {"bleed", "bled"}, {"breed", "bred"}, {"creep", "crept"}, {"flee", "fled"},
			{"do", "did"}, {"have", "had"}, {"undo", "undid"}, {"overcome", "overcame"}, {"foresee", "foresaw"},
			{"mislead", "misled"}, {"uphold", "upheld"}, {"withhold", "withheld"}, {"outgrow", "outgrew"},
			{"overtake", "overtook"}, {"arise", "arose"}, {"undergo", "underwent"}, {"wake", "woke"},
		},
	}

	siBank = bank{
		cat: CatSIUnit,
		ask: "What is the SI unit of %s?",
		fewshot: []QA{
			{Q: "What is the SI unit of energy?", A: "joule"},
			{Q: "What is the SI unit of frequency?", A: "hertz"},
		},
		facts: []fact{
			{"force", "newton"}, {"power", "watt"}, {"pressure", "pascal"}, {"electric charge", "coulomb"},
			{"electric current", "ampere"}, {"voltage", "volt"}, {"electric resistance", "ohm"},
			{"capacitance", "farad"}, {"inductance", "henry"}, {"magnetic flux density", "tesla"},
			{"magnetic flux", "weber"}, {"luminous intensity", "candela"}, {"amount of substance", "mole"},
			{"thermodynamic temperature", "kelvin"}, {"time", "second"}, {"mass", "kilogram"},
			{"electrical conductance", "siemens"}, {"radioactivity", "becquerel"},
			{"absorbed dose of radiation", "gray"}, {"equivalent dose of radiation", "sievert"},
			{"illuminance", "lux"}, {"luminous flux", "lumen"}, {"catalytic activity", "katal"},
			{"plane angle", "radian"}, {"solid angle", "steradian"},
		},
	}

	isoBank = bank{
		cat: CatISO,
		ask: "What is the ISO code of the %s?",
		fewshot: []QA{
			{Q: "What is the ISO code of the euro?", A: "EUR"},
			{Q: "What is the ISO code of the Canadian dollar?", A: "CAD"},
		},
		facts: []fact{
			{"US dollar", "USD"}, {"Japanese yen", "JPY"}, {"British pound", "GBP"}, {"Swiss franc", "CHF"},
			{"Australian dollar", "AUD"}, {"New Zealand dollar", "NZD"}, {"Chinese yuan", "CNY"},
			{"Hong Kong dollar", "HKD"}, {"Singapore dollar", "SGD"}, {"South Korean won", "KRW"},
			{"Indian rupee", "INR"}, {"Indonesian rupiah", "IDR"}, {"Thai baht", "THB"},
			{"Malaysian ringgit", "MYR"}, {"Philippine peso", "PHP"}, {"Vietnamese dong", "VND"},
			{"New Taiwan dollar", "TWD"}, {"Pakistani rupee", "PKR"}, {"Bangladeshi taka", "BDT"},
			{"Sri Lankan rupee", "LKR"}, {"Nepalese rupee", "NPR"}, {"Russian ruble", "RUB"},
			{"Ukrainian hryvnia", "UAH"}, {"Polish zloty", "PLN"}, {"Czech koruna", "CZK"},
			{"Hungarian forint", "HUF"}, {"Romanian leu", "RON"}, {"Bulgarian lev", "BGN"},
			{"Swedish krona", "SEK"}, {"Norwegian krone", "NOK"}, {"Danish krone", "DKK"},
			{"Icelandic krona", "ISK"}, {"Turkish lira", "TRY"}, {"Israeli shekel", "ILS"},
			{"Saudi riyal", "SAR"}, {"UAE dirham", "AED"}, {"Qatari riyal", "QAR"}, {"Kuwaiti dinar", "KWD"},
			{"Egyptian pound", "EGP"}, {"South African rand", "ZAR"}, {"Nigerian naira", "NGN"},
			{"Kenyan shilling", "KES"}, {"Ghanaian cedi", "GHS"}, {"Moroccan dirham", "MAD"},
			{"Brazilian real", "BRL"}, {"Argentine peso", "ARS"}, {"Chilean peso", "CLP"},
			{"Colombian peso", "COP"}, {"Peruvian sol", "PEN"}, {"Mexican peso", "MXN"},
			{"Uruguayan peso", "UYU"}, {"Jordanian dinar", "JOD"}, {"Omani rial", "OMR"},
			{"Bahraini dinar", "BHD"}, {"Georgian lari", "GEL"}, {"Kazakhstani tenge", "KZT"},
		},
	}

	domainBank = bank{
		cat: CatDomain,
		ask: "What is the internet country code of %s?",
		fewshot: []QA{
			{Q: "What is the internet country code of Germany?", A: ".de"},
			{Q: "What is the internet country code of France?", A: ".fr"},
		},
		facts: []fact{
			{"Japan", ".jp"}, {"the United Kingdom", ".uk"}, {"Spain", ".es"}, {"Canada", ".ca"},
			{"Australia", ".au"}, {"New Zealand", ".nz"}, {"China", ".cn"}, {"Brazil", ".br"},
			{"Russia", ".ru"}, {"South Korea", ".kr"}, {"Mexico", ".mx"}, {"the Netherlands", ".nl"},
			{"Switzerland", ".ch"}, {"Sweden", ".se"}, {"Denmark", ".dk"}, {"Finland", ".fi"},
			{"Poland", ".pl"}, {"the Czech Republic", ".cz"}, {"Hungary", ".hu"}, {"Greece", ".gr"},
			{"Portugal", ".pt"}, {"Ireland", ".ie"}, {"Turkey", ".tr"}, {"Israel", ".il"}, {"Egypt", ".eg"},
			{"South Africa", ".za"}, {"Nigeria", ".ng"}, {"Kenya", ".ke"}, {"Morocco", ".ma"},
			{"Argentina", ".ar"}, {"Chile", ".cl"}, {"Colombia", ".co"}, {"Peru", ".pe"},
			{"Venezuela", ".ve"}, {"Thailand", ".th"}, {"Vietnam", ".vn"}, {"Indonesia", ".id"},
			{"Singapore", ".sg"}, {"the Philippines", ".ph"}, {"Pakistan", ".pk"}, {"Bangladesh", ".bd"},
			{"Iran", ".ir"}, {"Iraq", ".iq"}, {"Saudi Arabia", ".sa"}, {"Ukraine", ".ua"},
			{"Romania", ".ro"}, {"Bulgaria", ".bg"}, {"Croatia", ".hr"}, {"Serbia", ".rs"},
			{"Slovakia", ".sk"}, {"Slovenia", ".si"}, {"Lithuania", ".lt"}, {"Latvia", ".lv"},
			{"Estonia", ".ee"}, {"Luxembourg", ".lu"}, {"Cuba", ".cu"}, {"Jamaica", ".jm"},
			{"Uruguay", ".uy"}, {"Ecuador", ".ec"}, {"Bolivia", ".bo"}, {"Paraguay", ".py"},
			{"Taiwan", ".tw"}, {"Kazakhstan", ".kz"}, {"Ghana", ".gh"}, {"Ethiopia", ".et"},
			{"Tanzania", ".tz"}, {"Algeria", ".dz"}, {"Tunisia", ".tn"}, {"Nepal", ".np"},
			{"Sri Lanka", ".lk"}, {"Afghanistan", ".af"}, {"Cambodia", ".kh"}, {"Mongolia", ".mn"},
		},
	}
)

// banks is every table, for the code categories below and for the tests.
var banks = []bank{verbBank, siBank, isoBank, domainBank}

func newVerbQuestion(rng *rand.Rand) Question   { return verbBank.question(rng) }
func newSIUnitQuestion(rng *rand.Rand) Question { return siBank.question(rng) }
func newISOQuestion(rng *rand.Rand) Question    { return isoBank.question(rng) }
func newDomainQuestion(rng *rand.Rand) Question { return domainBank.question(rng) }
