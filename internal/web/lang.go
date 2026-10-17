package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The languages PinPoint's pages speak. English is the one it falls back to.
// The Markdown and JSON a coding AI reads stay in English whatever the
// reviewer reads, as that is what those tools follow best.
const (
	english = "en"
	german  = "de"
)

// language is what to show this request in: what the visitor picked with the
// switch, if they did, or else whichever of English and German their browser
// prefers.
func language(r *http.Request) string {
	if c, err := r.Cookie("lang"); err == nil && (c.Value == english || c.Value == german) {
		return c.Value
	}
	best, bestQ := english, 0.0
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, params, _ := strings.Cut(part, ";")
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			q, _ = strconv.ParseFloat(v, 64) // nonsense counts as not wanted
		}
		primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		if (primary == english || primary == german) && q > bestQ {
			best, bestQ = primary, q
		}
	}
	return best
}

// tr says s in lang, filling in args as fmt does. Anything without a
// translation is said in English, which is better than saying nothing.
func tr(lang, s string, args ...any) string {
	if lang == german {
		if d, ok := germanText[s]; ok {
			s = d
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// langFuncs are the template functions that depend on the language. Each
// language gets its own copy of the templates, so a page never has to pass
// its language around.
func langFuncs(lang string) template.FuncMap {
	return template.FuncMap{
		"lang": func() string { return lang },
		"t":    func(s string, args ...any) string { return tr(lang, s, args...) },
		// th is for translations that carry their own markup. Only ever
		// for text written into the templates, never for anything a
		// visitor typed.
		"th": func(s string) template.HTML { return template.HTML(tr(lang, s)) },
		"plural": func(n int, word string) string {
			if n == 1 {
				return tr(lang, "1 "+word)
			}
			return tr(lang, "%d "+word+"s", n)
		},
		"date": func(t time.Time) string {
			t = t.Local()
			if lang == german {
				return fmt.Sprintf("%d. %s %d", t.Day(), germanMonths[t.Month()-1], t.Year())
			}
			return t.Format("2 January 2006")
		},
		"device": func(width int) string { return fmt.Sprintf("%s (%dpx)", tr(lang, deviceKind(width)), width) },
		// texts is what the page scripts need to speak the language.
		"texts": func() map[string]string {
			if lang == german {
				return germanText
			}
			return map[string]string{}
		},
	}
}

var germanMonths = [...]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August",
	"September", "Oktober", "November", "Dezember"}

// germanText is every sentence PinPoint shows, in German. The page scripts
// get it too, and fill in {0} where Go fills in %s and %d.
var germanText = map[string]string{
	// Start page
	"Review a website": "Feedback zu einer Website",
	"Mark up a website, and send the feedback as a link a coding AI can act on.":      "Markieren Sie eine Website und senden Sie das Feedback als Link, mit dem eine Coding-KI arbeiten kann.",
	"Point at what you'd like changed, and send the link to whoever builds the site.": "Zeigen Sie auf das, was sich ändern soll, und schicken Sie den Link an die Person, die die Website baut.",
	"Website address": "Website-Adresse",
	"Start reviewing": "Feedback starten",
	"The site opens with a toolbar. Browse it as usual.":                                      "Die Website öffnet sich mit einer Werkzeugleiste. Bewegen Sie sich darin wie gewohnt.",
	"Switch to <strong>Annotate</strong> and click, highlight, box or draw on anything.":      "Wechseln Sie zu <strong>Markieren</strong> und klicken Sie etwas an, heben Sie Text hervor, ziehen Sie einen Rahmen oder zeichnen Sie.",
	"Press <strong>Apply</strong> and send the review link. The agency's coding AI reads it.": "Klicken Sie auf <strong>Absenden</strong> und schicken Sie den Link weiter. Die Coding-KI der Agentur liest ihn.",
	"Your reviews in this browser":                                                            "Ihr Feedback in diesem Browser",
	"1 annotation":                                                                            "1 Anmerkung",
	"%d annotations":                                                                          "%d Anmerkungen",
	"{0} annotations":                                                                         "{0} Anmerkungen",
	"sent":                                                                                    "gesendet",
	"draft":                                                                                   "Entwurf",

	// Switches
	"Switch between light and dark": "Zwischen hell und dunkel wechseln",

	// Editor
	"Reviewing %s":    "Feedback zu %s",
	"Back":            "Zurück",
	"Reload the page": "Seite neu laden",
	"Page address":    "Seitenadresse",
	"Mode":            "Modus",
	"Browse":          "Browsen",
	"Browse: click links and scroll as usual (B)": "Browsen: Links anklicken und scrollen wie gewohnt (B)",
	"Annotate":                           "Markieren",
	"Annotate: clicks mark the page (A)": "Markieren: Klicks markieren die Seite (A)",
	"Annotation tool":                    "Markierwerkzeug",
	"Comment":                            "Kommentar",
	"Highlight":                          "Textmarker",
	"Box":                                "Rahmen",
	"Drawing":                            "Zeichnung",
	"Apply":                              "Absenden",
	"The website being reviewed":         "Die Website, zu der Sie Feedback geben",
	"Loading the page":                   "Die Seite wird geladen",
	"Go back":                            "Zurück",
	"Annotations":                        "Anmerkungen",
	"Close the list":                     "Liste schließen",
	"Sent. Changes you make now show up at the same link.": "Gesendet. Was Sie jetzt noch ändern, erscheint unter demselben Link.",
	"Apply and get the link":                               "Absenden und Link erhalten",
	"What kind of feedback":                                "Art des Feedbacks",
	"Change":                                               "Ändern",
	"Bug":                                                  "Fehler",
	"Question":                                             "Frage",
	"Like it":                                              "Gefällt mir",
	"What should change?":                                  "Was soll sich ändern?",
	"Cancel":                                               "Abbrechen",
	"Save":                                                 "Speichern",
	"Send your review":                                     "Feedback senden",
	"Your name":                                            "Ihr Name",
	"(so they know who it's from)":                         "(damit man weiß, von wem es kommt)",
	"Keep reviewing":                                       "Weiter markieren",
	"Your review is ready":                                 "Ihr Feedback ist fertig",
	"Send this link to whoever builds the website. They can paste it straight into their coding AI.": "Schicken Sie diesen Link an die Person, die die Website baut. Sie kann ihn direkt in ihre Coding-KI einfügen.",
	"Review link":     "Feedback-Link",
	"Copy":            "Kopieren",
	"Copy for AI":     "Für KI kopieren",
	"Open the review": "Feedback öffnen",
	"You can keep changing this review: the link always shows the latest. To come back to it, use this page's address, which only you have.": "Sie können dieses Feedback weiter ändern: Der Link zeigt immer den neuesten Stand. Um später weiterzumachen, nutzen Sie die Adresse dieser Seite, die nur Sie haben.",
	"Done": "Fertig",

	// Shared review
	"Review of %s":               "Feedback zu %s",
	"%s on %s":                   "%s zu %s",
	"from %s":                    "von %s",
	"on %d pages":                "auf %d Seiten",
	"sent %s":                    "gesendet am %s",
	"draft, still being written": "Entwurf, wird noch geschrieben",
	"Copy the whole review as text to paste into a coding AI": "Das ganze Feedback als Text kopieren, um es in eine Coding-KI einzufügen",
	"Download as Markdown":                                  "Als Markdown herunterladen",
	"Download as JSON":                                      "Als JSON herunterladen",
	"The website, with the annotations on it":               "Die Website mit den Anmerkungen",
	"Annotation %d":                                         "Anmerkung %d",
	"The element this was attached to has moved or is gone": "Das Element, an dem dies hing, hat sich verschoben oder ist weg",
	"moved":                        "verschoben",
	"No comment; see the mark.":    "Kein Kommentar; siehe Markierung.",
	"Screenshot of annotation %d":  "Screenshot von Anmerkung %d",
	"Element details":              "Details zum Element",
	"text":                         "Text",
	"Nothing has been marked yet.": "Noch nichts markiert.",
	"Text for your coding AI":      "Text für Ihre Coding-KI",
	"Paste the link, or this text, into your coding AI. Plain-text versions:": "Fügen Sie den Link oder diesen Text in Ihre Coding-KI ein. Als reiner Text:",
	"Home page":  "Startseite",
	"desktop":    "Desktop",
	"tablet":     "Tablet",
	"mobile":     "Smartphone",
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

	// Messages from the server
	"This request came from another site.": "Diese Anfrage kam von einer anderen Website.",
	"Something went wrong.":                "Etwas ist schiefgelaufen.",
	"Something went wrong":                 "Etwas ist schiefgelaufen",
	"Something went wrong. Try again.":     "Etwas ist schiefgelaufen. Versuchen Sie es noch einmal.",
	"Try again in a moment.":               "Versuchen Sie es gleich noch einmal.",
	"Nothing here":                         "Hier ist nichts",
	"This link doesn't lead anywhere. If somebody sent it to you, ask them for it again.": "Dieser Link führt nirgendwohin. Wenn Ihnen jemand diesen Link geschickt hat, bitten Sie um einen neuen.",
	"That's a lot of reviews in a short time. Wait a minute, then try again.":             "Das ist viel Feedback in kurzer Zeit. Warten Sie eine Minute und versuchen Sie es dann noch einmal.",
	"Too many pages in a short time. Wait a minute, then try again.":                      "Zu viele Seiten in kurzer Zeit. Warten Sie eine Minute und versuchen Sie es dann noch einmal.",
	"Type the address of the website to review.":                                          "Geben Sie die Adresse der Website ein.",
	"That doesn't look like a website address. It should look like example.com.":          "Das sieht nicht nach einer Website-Adresse aus. Sie sollte etwa so aussehen: example.com.",
	"This edit link doesn't lead to a review.":                                            "Dieser Bearbeitungslink führt zu keinem Feedback.",
	"This review isn't here": "Dieses Feedback gibt es hier nicht",
	"The edit link may be incomplete. Check you copied all of it.":        "Der Bearbeitungslink ist vielleicht unvollständig. Prüfen Sie, ob Sie ihn ganz kopiert haben.",
	"The link may be incomplete. Ask whoever sent it for the whole link.": "Der Link ist vielleicht unvollständig. Bitten Sie die Person, die ihn geschickt hat, um den ganzen Link.",
	"That request didn't make sense.":                                     "Diese Anfrage ergab keinen Sinn.",
	"That annotation is gone.":                                            "Diese Anmerkung gibt es nicht mehr.",
	"That annotation couldn't be saved.":                                  "Diese Anmerkung konnte nicht gespeichert werden.",
	"That screenshot is too large.":                                       "Dieser Screenshot ist zu groß.",
	"Mark at least one thing on the page first.":                          "Markieren Sie zuerst mindestens eine Stelle auf der Seite.",

	// In the frame
	"This review is gone":                                            "Dieses Feedback gibt es nicht mehr",
	"The link may be wrong, or the review was deleted.":              "Der Link ist vielleicht falsch, oder das Feedback wurde gelöscht.",
	"That isn't a web page":                                          "Das ist keine Webseite",
	"Only web pages can be reviewed.":                                "Feedback ist nur zu Webseiten möglich.",
	"That's another website":                                         "Das ist eine andere Website",
	"This review is for %s. Pages on other sites open in a new tab.": "Dieses Feedback gilt %s. Seiten anderer Websites öffnen sich in einem neuen Tab.",
	"Couldn't load the page":                                         "Die Seite konnte nicht geladen werden",
	"That page leads to another website":                             "Diese Seite führt zu einer anderen Website",
	"It sends visitors on to %s, which isn't part of this review.":   "Sie leitet Besucher zu %s weiter, das nicht zu diesem Feedback gehört.",
	"Open it in a new tab":                                           "In neuem Tab öffnen",
	"That address is on a private network, which PinPoint can't reach. Staging and local sites need the script snippet.": "Diese Adresse liegt in einem privaten Netzwerk, das PinPoint nicht erreicht. Staging- und lokale Websites brauchen das Skript-Snippet.",
	"There's no website at that address. Check it for typos.":                                                            "Unter dieser Adresse gibt es keine Website. Prüfen Sie sie auf Tippfehler.",
	"The website took too long to answer.":                                                                               "Die Website hat zu lange nicht geantwortet.",
	"That page is too large to review.":                                                                                  "Diese Seite ist zu groß für ein Feedback.",
	"The website's security certificate isn't valid.":                                                                    "Das Sicherheitszertifikat der Website ist ungültig.",
	"The website keeps redirecting and never arrives at a page.":                                                         "Die Website leitet immer weiter um und kommt nie bei einer Seite an.",
	"Couldn't reach the website.":                                                                                        "Die Website war nicht erreichbar.",

	// Page scripts
	"Click anything to comment on it":                                 "Klicken Sie etwas an, um es zu kommentieren",
	"Select text to highlight it, or drag across an area":             "Wählen Sie Text aus, um ihn hervorzuheben, oder ziehen Sie über einen Bereich",
	"Drag a box, or click something to frame it":                      "Ziehen Sie einen Rahmen auf oder klicken Sie etwas an, um es einzurahmen",
	"Draw on the page":                                                "Zeichnen Sie auf die Seite",
	"This page left the review, so it can't be shown or marked here.": "Diese Seite hat das Feedback verlassen und kann hier nicht angezeigt oder markiert werden.",
	"Copied": "Kopiert",
	"Couldn't copy. Select the text and copy it yourself.": "Kopieren ging nicht. Markieren Sie den Text und kopieren Sie ihn selbst.",
	"Copied. Paste it into your coding AI.":                "Kopiert. Fügen Sie es in Ihre Coding-KI ein.",
	"Couldn't get the review text. Try again.":             "Der Feedback-Text konnte nicht geladen werden. Versuchen Sie es noch einmal.",
	"Taking a screenshot…":                                 "Screenshot wird aufgenommen …",
	"Screenshot taken":                                     "Screenshot aufgenommen",
	"No screenshot: this page couldn't be photographed":    "Kein Screenshot: Diese Seite ließ sich nicht fotografieren",
	"Nothing marked yet":                                   "Noch nichts markiert",
	"Pick a tool above and click, select, box or draw on the page. Each mark gets a comment.": "Wählen Sie oben ein Werkzeug und klicken, markieren, umrahmen oder zeichnen Sie auf der Seite. Jede Markierung bekommt einen Kommentar.",
	"Annotation {0}":                "Anmerkung {0}",
	"No comment; the mark says it.": "Kein Kommentar; die Markierung spricht für sich.",
	"Edit":                          "Bearbeiten",
	"Edit annotation {0}":           "Anmerkung {0} bearbeiten",
	"Delete":                        "Löschen",
	"Delete annotation {0}":         "Anmerkung {0} löschen",
	"Delete annotation {0}?":        "Anmerkung {0} löschen?",
	"on {0} pages":                  "auf {0} Seiten",
	"You get a link to send to whoever builds the site.":     "Sie erhalten einen Link für die Person, die die Website baut.",
	"That isn't a web address.":                              "Das ist keine Web-Adresse.",
	"This review is for {0}. Other sites open in a new tab.": "Dieses Feedback gilt {0}. Andere Websites öffnen sich in einem neuen Tab.",
	"Link copied": "Link kopiert",
	"Downloads are switched off while reviewing.":                     "Downloads sind beim Feedback ausgeschaltet.",
	"Forms aren't sent while reviewing, so nothing reached the site.": "Formulare werden beim Feedback nicht gesendet, die Website hat also nichts erhalten.",
	"Select some text to highlight it, or drag across an area.":       "Wählen Sie Text aus, um ihn hervorzuheben, oder ziehen Sie über einen Bereich.",
	"The element this was on has moved or is gone.":                   "Das Element, an dem dies war, hat sich verschoben oder ist weg.",
}
