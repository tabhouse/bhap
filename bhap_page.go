package main

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
	blackfriday "gopkg.in/russross/blackfriday.v2"
)

var bhapTemplate = compileTempl("views/bhap.html")

type optionsMode string

const (
	notLoggedIn      optionsMode = "notLoggedIn"
	draftNotAuthor               = "draftNotAuthor"
	draftAuthor                  = "draftAuthor"
	discussionAuthor             = "discussionAuthor"
	discussionNoVote             = "discussionNoVote"
	discussionVoted              = "discussionVoted"
	finalized                    = "finalized"
)

// bhapPageFiller fills the BHAP viewer page template.
type bhapPageFiller struct {
	LoggedIn     bool
	FullName     string
	ID           int
	BHAP         bhap
	SelectedVote string
	OptionsMode  optionsMode
	Editable     bool
	HTMLContent  template.HTML

	VoteCount int
	UserCount int

	PercentAccepted  int
	PercentRejected  int
	PercentUndecided int
}

// serveBHAPPage serves up a page that displays info on a single BHAP.
func serveBHAPPage(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)

	// Get the requested ID
	idStr := mux.Vars(r)["id"]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		log.Warningf(ctx, "invalid ID %v: %v", idStr, err)
		http.Error(w, "ID must be an integer", 400)
		return
	}

	// Load the requested BHAP
	loadedBHAP, bhapKey, err := bhapByID(ctx, id)
	if err != nil {
		log.Errorf(ctx, "could not load BHAP: %v", err)
		http.Error(w, "Failed to load BHAP", http.StatusInternalServerError)
		return
	}
	if bhapKey == nil {
		http.Error(w, fmt.Sprintf("No BHAP with ID %v", id), 404)
		log.Warningf(ctx, "unknown BHAP requested: %v", id)
		return
	}

	// Render the BHAP content
	options := blackfriday.WithExtensions(blackfriday.HardLineBreak)
	html := string(blackfriday.Run([]byte(loadedBHAP.Content), options))

	var author user
	if err := datastore.Get(ctx, loadedBHAP.Author, &author); err != nil {
		log.Errorf(ctx, "loading user: %v", err)
		http.Error(w, "Failed to load user", http.StatusInternalServerError)
		return
	}

	// Get the current logged in user
	user, userKey, err := userFromSession(ctx, r)
	if err != nil {
		http.Error(w, "Could not read session", http.StatusInternalServerError)
		log.Errorf(ctx, "getting session email: %v", err)
		return
	}

	allVotes, err := allVotesForBHAP(ctx, bhapKey)
	if err != nil {
		http.Error(w, "Could not get votes",
			http.StatusInternalServerError)
		log.Errorf(ctx, "getting votes: %v", err)
		return
	}

	userCount, err := datastore.NewQuery(userEntityName).Count(ctx)
	if err != nil {
		http.Error(w, "Could not get user count",
			http.StatusInternalServerError)
		log.Errorf(ctx, "getting user count: %v", err)
		return
	}

	usersVote, usersVoteKey, err := voteForBHAP(ctx, bhapKey, userKey)
	if err != nil {
		http.Error(w, "Could not read user's vote",
			http.StatusInternalServerError)
		log.Errorf(ctx, "getting user's vote: %v", err)
		return
	}

	// Decide what options the user should have
	var mode optionsMode
	if userKey == nil {
		mode = notLoggedIn
	} else if loadedBHAP.Status == draftStatus {
		if userKey.Equal(loadedBHAP.Author) {
			mode = draftAuthor
		} else {
			mode = draftNotAuthor
		}
	} else if loadedBHAP.Status == discussionStatus {
		if userKey.Equal(loadedBHAP.Author) {
			mode = discussionAuthor
		} else {
			if usersVoteKey == nil {
				mode = discussionNoVote
			} else {
				mode = discussionVoted
			}
		}
	} else {
		mode = finalized
	}

	// Figure out the vote breakdown
	acceptedCount := 0
	rejectedCount := 0
	for _, v := range allVotes {
		if v.Value == acceptedStatus {
			acceptedCount++
		} else if v.Value == rejectedStatus {
			rejectedCount++
		}
	}
	undecidedCount := userCount - (acceptedCount + rejectedCount) - 1

	var fullName string
	if userKey != nil {
		fullName = user.FirstName + " " + user.LastName
	}

	var selectedVote string
	if usersVoteKey != nil {
		if usersVote.Value == acceptedStatus {
			selectedVote = "ACCEPT"
		} else if usersVote.Value == rejectedStatus {
			selectedVote = "REJECTED"
		} else {
			http.Error(w, "Unknown vote type",
				http.StatusInternalServerError)
			log.Errorf(ctx, "unknown vote type %v", usersVote.Value)
			return
		}
	}

	filler := bhapPageFiller{
		LoggedIn:     userKey != nil,
		FullName:     fullName,
		ID:           loadedBHAP.ID,
		BHAP:         loadedBHAP,
		OptionsMode:  mode,
		SelectedVote: selectedVote,
		Editable:     isEditable(loadedBHAP.Status),
		HTMLContent:  template.HTML(html),

		VoteCount: len(allVotes),
		UserCount: userCount - 1,

		PercentAccepted:  int((acceptedCount / (userCount - 1)) * 100),
		PercentRejected:  int((rejectedCount / (userCount - 1)) * 100),
		PercentUndecided: int((undecidedCount / (userCount - 1)) * 100),
	}
	log.Infof(ctx, "Filler: %+v", filler)
	showTemplate(ctx, w, bhapTemplate, filler)
}
