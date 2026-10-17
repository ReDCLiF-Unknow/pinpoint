package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// lang is the language PinPoint's pages are shown in. The English text is
// the key: German looks it up, and anything without a translation stays
// English. What a coding AI reads (the review as Markdown or JSON) is always
// English.
type lang string

const (
	en lang = "en"
	de lang = "de"
)

var langs = []lang{en, de}

// langOf is the language for a request: the one picked with the switch on
// the page (a cookie), or else the first of ours the browser asks for.
func langOf(r *http.Request) lang {
	if c, err := r.Cookie("lang"); err == nil {
		for _, l := range langs {
			if c.Value == string(l) {
				return l
			}
		}
	}
	best, bestQ := en, 0.0
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		base, _, _ := strings.Cut(strings.ToLower(tag), "-")
		for _, l := range langs {
			if base == string(l) && q > bestQ {
				best, bestQ = l, q
			}
		}
	}
	return best
}

// T translates English text.
func (l lang) T(s string) string {
	if l == de {
		if t, ok := german[s]; ok {
			return t
		}
	}
	return s
}

// Tf translates a format string, then fills it in.
func (l lang) Tf(format string, args ...any) string { return fmt.Sprintf(l.T(format), args...) }

// text is what the page scripts translate with: nothing for English.
func (l lang) text() map[string]string {
	if l == de {
		return german
	}
	return nil
}

func (l lang) plural(n int, word string) string {
	if l == de {
		if forms, ok := germanPlurals[word]; ok {
			if n == 1 {
				return "1 " + forms[0]
			}
			return strconv.Itoa(n) + " " + forms[1]
		}
	}
	return plural(n, word)
}

func (l lang) date(t time.Time) string {
	t = t.Local()
	if l == de {
		return fmt.Sprintf("%d. %s %d", t.Day(), germanMonths[t.Month()-1], t.Year())
	}
	return t.Format("2 January 2006")
}

// device is device(), in this language.
func (l lang) device(width int) string {
	kind, _, _ := strings.Cut(device(width), " ")
	return fmt.Sprintf("%s (%dpx)", l.T(kind), width)
}

// langFuncs are the template functions that depend on the language.
func langFuncs(l lang) template.FuncMap {
	return template.FuncMap{
		"lang": func() string { return string(l) },
		"t":    l.T,
		// th is for translations that hold markup. Both sides are ours.
		"th":     func(s string) template.HTML { return template.HTML(l.T(s)) },
		"tf":     l.Tf,
		"plural": l.plural,
		"date":   l.date,
		"device": l.device,
	}
}

var germanMonths = [12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August",
	"September", "Oktober", "November", "Dezember"}

var germanPlurals = map[string][2]string{
	"annotation": {"Anmerkung", "Anmerkungen"},
}

// german is every piece of text PinPoint shows, in German. The customer is
// addressed formally ("Sie").
var german = map[string]string{
	// Labels, tools, devices, pages
	"Change":    "Änderung",
	"Bug":       "Fehler",
	"Question":  "Frage",
	"Like it":   "Gefällt mir",
	"Comment":   "Kommentar",
	"Highlight": "Hervorheben",
	"Box":       "Rahmen",
	"Drawing":   "Zeichnung",
	"desktop":   "Desktop",
	"tablet":    "Tablet",
	"mobile":    "Mobil",
	"Home page": "Startseite",

	// What sort of element something is
	"link":       "Link",
	"button":     "Schaltfläche",
	"image":      "Bild",
	"heading":    "Überschrift",
	"paragraph":  "Absatz",
	"form field": "Formularfeld",
	"list":       "Liste",
	"list item":  "Listeneintrag",
	"navigation": "Navigation",
	"header":     "Kopfbereich",
	"footer":     "Fußbereich",
	"section":    "Abschnitt",
	"form":       "Formular",
	"table":      "Tabelle",
	"video":      "Video",
	"label":      "Beschriftung",

	// Everywhere
	"Switch between light and dark":        "Zwischen hell und dunkel wechseln",
	"Review a website":                     "Feedback zu einer Website geben",
	"Something went wrong.":                "Etwas ist schiefgegangen.",
	"Something went wrong":                 "Etwas ist schiefgegangen",
	"Something went wrong. Try again.":     "Etwas ist schiefgegangen. Versuchen Sie es erneut.",
	"Try again in a moment.":               "Versuchen Sie es gleich noch einmal.",
	"This request came from another site.": "Diese Anfrage kam von einer anderen Website.",
	"Nothing here":                         "Hier ist nichts",
	"This link doesn't lead anywhere. If somebody sent it to you, ask them for it again.": "Dieser Link führt nirgendwohin. Wenn Sie ihn von jemandem bekommen haben, bitten Sie noch einmal darum.",
	"This review isn't here": "Dieses Feedback gibt es hier nicht",
	"The link may be incomplete. Ask whoever sent it for the whole link.": "Der Link ist vielleicht unvollständig. Bitten Sie die Person, die ihn geschickt hat, um den vollständigen Link.",
	"The edit link may be incomplete. Check you copied all of it.":        "Der Bearbeitungslink ist vielleicht unvollständig. Prüfen Sie, ob Sie ihn ganz kopiert haben.",

	// Start page
	"Mark up a website, and send the feedback as a link a coding AI can act on.":      "Markieren Sie eine Website und senden Sie das Feedback als Link, mit dem eine Coding-KI arbeiten kann.",
	"Point at what you'd like changed, and send the link to whoever builds the site.": "Zeigen Sie, was sich ändern soll, und senden Sie den Link an die Entwickler Ihrer Website.",
	"Website address": "Adresse der Website",
	"Start reviewing": "Feedback starten",
	"The site opens with a toolbar. Browse it as usual.":                                      "Die Website öffnet sich mit einer Werkzeugleiste. Nutzen Sie sie wie gewohnt.",
	"Switch to <strong>Annotate</strong> and click, highlight, box or draw on anything.":      "Wechseln Sie zu <strong>Markieren</strong> und klicken, markieren, umrahmen oder zeichnen Sie auf alles, was Ihnen auffällt.",
	"Press <strong>Apply</strong> and send the review link. The agency's coding AI reads it.": "Klicken Sie auf <strong>Absenden</strong> und schicken Sie den Link weiter. Die Coding-KI Ihrer Agentur liest ihn.",
	"Your reviews in this browser":                                                            "Ihr Feedback in diesem Browser",
	"1 annotation":                                                                            "1 Anmerkung",
	"{n} annotations":                                                                         "{n} Anmerkungen",
	"sent":                                                                                    "gesendet",
	"draft":                                                                                   "Entwurf",
	"That's a lot of reviews in a short time. Wait a minute, then try again.":    "Das ist viel Feedback in kurzer Zeit. Warten Sie eine Minute und versuchen Sie es dann erneut.",
	"Too many pages in a short time. Wait a minute, then try again.":             "Zu viele Seiten in kurzer Zeit. Warten Sie eine Minute und versuchen Sie es dann erneut.",
	"Type the address of the website to review.":                                 "Geben Sie die Adresse der Website ein, zu der Sie Feedback geben möchten.",
	"That doesn't look like a website address. It should look like example.com.": "Das sieht nicht nach einer Website-Adresse aus. Sie sollte etwa so aussehen: example.com.",

	// Why a site couldn't be loaded (proxy.Explain)
	"That address is on a private network, which PinPoint can't reach. Staging and local sites need the script snippet.": "Diese Adresse liegt in einem privaten Netzwerk, das PinPoint nicht erreicht. Staging- und lokale Websites brauchen das Script-Snippet.",
	"There's no website at that address. Check it for typos.":                                                            "Unter dieser Adresse gibt es keine Website. Prüfen Sie sie auf Tippfehler.",
	"The website took too long to answer.":                                                                               "Die Website hat zu lange nicht geantwortet.",
	"That page is too large to review.":                                                                                  "Diese Seite ist zu groß für ein Feedback.",
	"The website's security certificate isn't valid.":                                                                    "Das Sicherheitszertifikat der Website ist ungültig.",
	"The website keeps redirecting and never arrives at a page.":                                                         "Die Website leitet immer weiter um und kommt nie bei einer Seite an.",
	"Couldn't reach the website.":                                                                                        "Die Website ist nicht erreichbar.",

	// The frame
	"Open it in a new tab":                                            "In neuem Tab öffnen",
	"This review is gone":                                             "Dieses Feedback gibt es nicht mehr",
	"The link may be wrong, or the review was deleted.":               "Der Link ist vielleicht falsch, oder das Feedback wurde gelöscht.",
	"That isn't a web page":                                           "Das ist keine Webseite",
	"Only web pages can be reviewed.":                                 "Feedback ist nur zu Webseiten möglich.",
	"That's another website":                                          "Das ist eine andere Website",
	"This review is for %s. Pages on other sites open in a new tab.":  "Dieses Feedback gilt für %s. Seiten anderer Websites öffnen sich in einem neuen Tab.",
	"Couldn't load the page":                                          "Die Seite konnte nicht geladen werden",
	"That page leads to another website":                              "Diese Seite führt zu einer anderen Website",
	"It sends visitors on to %s, which isn't part of this review.":    "Sie leitet Besucher zu %s weiter, das nicht zu diesem Feedback gehört.",
	"Downloads are switched off while reviewing.":                     "Downloads sind beim Feedbackgeben ausgeschaltet.",
	"Forms aren't sent while reviewing, so nothing reached the site.": "Formulare werden beim Feedbackgeben nicht abgeschickt; bei der Website ist nichts angekommen.",
	"The element this was on has moved or is gone.":                   "Das Element, an dem dies war, hat sich verschoben oder ist weg.",
	"Select some text to highlight it, or drag across an area.":       "Markieren Sie Text, um ihn hervorzuheben, oder ziehen Sie über einen Bereich.",

	// The editor
	"Reviewing %s":    "Feedback zu %s",
	"Back":            "Zurück",
	"Reload the page": "Seite neu laden",
	"Page address":    "Adresse der Seite",
	"Mode":            "Modus",
	"Browse":          "Surfen",
	"Browse: click links and scroll as usual (B)": "Surfen: Links anklicken und scrollen wie gewohnt (B)",
	"Annotate":                           "Markieren",
	"Annotate: clicks mark the page (A)": "Markieren: Klicks markieren die Seite (A)",
	"Annotation tool":                    "Werkzeug",
	"Apply":                              "Absenden",
	"The website being reviewed":         "Die Website, zu der Sie Feedback geben",
	"Loading the page":                   "Seite wird geladen",
	"Go back":                            "Zurück",
	"Annotations":                        "Anmerkungen",
	"Close the list":                     "Liste schließen",
	"Sent. Changes you make now show up at the same link.": "Gesendet. Was Sie jetzt noch ändern, erscheint unter demselben Link.",
	"Apply and get the link":                               "Absenden und Link erhalten",
	"What kind of feedback":                                "Art des Feedbacks",
	"What should change?":                                  "Was soll sich ändern?",
	"Cancel":                                               "Abbrechen",
	"Save":                                                 "Speichern",
	"Send your review":                                     "Feedback absenden",
	"Your name":                                            "Ihr Name",
	"(so they know who it's from)":                         "(damit man weiß, von wem es kommt)",
	"Optional":                                             "Optional",
	"Keep reviewing":                                       "Weiter bearbeiten",
	"Your review is ready":                                 "Ihr Feedback ist fertig",
	"Send this link to whoever builds the website. They can paste it straight into their coding AI.": "Senden Sie diesen Link an die Entwickler Ihrer Website. Sie können ihn direkt in ihre Coding-KI einfügen.",
	"Review link":     "Feedback-Link",
	"Copy":            "Kopieren",
	"Copy for AI":     "Für KI kopieren",
	"Open the review": "Feedback öffnen",
	"You can keep changing this review: the link always shows the latest. To come back to it, use this page's address, which only you have.": "Sie können dieses Feedback weiter ändern: Der Link zeigt immer den neuesten Stand. Um später daran weiterzuarbeiten, nutzen Sie die Adresse dieser Seite – nur Sie haben sie.",
	"Done": "Fertig",

	// The editor's script
	"Click anything to comment on it":                                 "Klicken Sie auf etwas, um es zu kommentieren",
	"Select text to highlight it, or drag across an area":             "Markieren Sie Text oder ziehen Sie über einen Bereich",
	"Drag a box, or click something to frame it":                      "Ziehen Sie einen Rahmen auf oder klicken Sie auf etwas, um es einzurahmen",
	"Draw on the page":                                                "Zeichnen Sie auf die Seite",
	"This page left the review, so it can't be shown or marked here.": "Diese Seite hat das Feedback verlassen und kann hier nicht angezeigt oder markiert werden.",
	"Copied": "Kopiert",
	"Couldn't copy. Select the text and copy it yourself.": "Kopieren hat nicht geklappt. Markieren Sie den Text und kopieren Sie ihn selbst.",
	"Copied. Paste it into your coding AI.":                "Kopiert. Fügen Sie es in Ihre Coding-KI ein.",
	"Couldn't get the review text. Try again.":             "Der Text des Feedbacks konnte nicht geladen werden. Versuchen Sie es erneut.",
	"Taking a screenshot…":                                 "Screenshot wird erstellt …",
	"Screenshot taken":                                     "Screenshot erstellt",
	"No screenshot: this page couldn't be photographed":    "Kein Screenshot: Diese Seite ließ sich nicht abfotografieren",
	"Nothing marked yet":                                   "Noch nichts markiert",
	"Pick a tool above and click, select, box or draw on the page. Each mark gets a comment.": "Wählen Sie oben ein Werkzeug und klicken, markieren, umrahmen oder zeichnen Sie auf der Seite. Jede Markierung bekommt einen Kommentar.",
	"Annotation {n}": "Anmerkung {n}",
	"The element this was attached to has moved or is gone": "Das Element, an dem dies hing, hat sich verschoben oder ist weg",
	"moved":                         "verschoben",
	"No comment; the mark says it.": "Kein Kommentar; die Markierung spricht für sich.",
	"Edit":                          "Bearbeiten",
	"Edit annotation {n}":           "Anmerkung {n} bearbeiten",
	"Delete":                        "Löschen",
	"Delete annotation {n}":         "Anmerkung {n} löschen",
	"Delete annotation {n}?":        "Anmerkung {n} löschen?",
	"Mark at least one thing on the page first.": "Markieren Sie zuerst mindestens eine Stelle auf der Seite.",
	"on {n} pages": "auf {n} Seiten",
	"You get a link to send to whoever builds the site.":        "Sie erhalten einen Link für die Entwickler Ihrer Website.",
	"That isn't a web address.":                                 "Das ist keine Web-Adresse.",
	"This review is for {host}. Other sites open in a new tab.": "Dieses Feedback gilt für {host}. Andere Websites öffnen sich in einem neuen Tab.",
	"Link copied": "Link kopiert",

	// Errors the editor shows
	"This edit link doesn't lead to a review.": "Dieser Bearbeitungslink führt zu keinem Feedback.",
	"That request didn't make sense.":          "Diese Anfrage ergab keinen Sinn.",
	"That annotation is gone.":                 "Diese Anmerkung gibt es nicht mehr.",
	"That annotation couldn't be saved.":       "Diese Anmerkung konnte nicht gespeichert werden.",
	"That screenshot is too large.":            "Dieser Screenshot ist zu groß.",
	"A screenshot has to be a PNG.":            "Ein Screenshot muss ein PNG sein.",

	// The shared review
	"Review of %s":               "Feedback zu %s",
	"%s on %s":                   "%s zu %s",
	"from %s":                    "von %s",
	"on %d pages":                "auf %d Seiten",
	"sent %s":                    "gesendet am %s",
	"draft, still being written": "Entwurf, wird noch geschrieben",
	"Copy the whole review as text to paste into a coding AI": "Das ganze Feedback als Text kopieren, um es in eine Coding-KI einzufügen",
	"Download as Markdown":                    "Als Markdown herunterladen",
	"Download as JSON":                        "Als JSON herunterladen",
	"The website, with the annotations on it": "Die Website mit den Anmerkungen",
	"Annotation %d":                           "Anmerkung %d",
	"No comment; see the mark.":               "Kein Kommentar; siehe Markierung.",
	"Screenshot of annotation %d":             "Screenshot von Anmerkung %d",
	"Element details":                         "Details zum Element",
	", text “%s”":                             ", Text „%s“",
	"Nothing has been marked yet.":            "Noch nichts markiert.",
	"Text for your coding AI":                 "Text für Ihre Coding-KI",
	"Paste the link, or this text, into your coding AI. Plain-text versions:": "Fügen Sie den Link oder diesen Text in Ihre Coding-KI ein. Als reiner Text:",
}
