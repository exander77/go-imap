package responses

import (
	"github.com/emersion/go-imap"
)

const (
	listName = "LIST"
	lsubName = "LSUB"
)

// A LIST response.
// If Subscribed is set to true, LSUB will be used instead.
// See RFC 3501 section 7.2.2
type List struct {
	Mailboxes  chan *imap.MailboxInfo
	Subscribed bool
	SpecialUse bool
}

func (r *List) Name() string {
	if r.Subscribed {
		return lsubName
	} else {
		return listName
	}
}

func (r *List) Handle(resp imap.Resp) error {
	name, fields, ok := imap.ParseNamedResp(resp)
	if !ok || name != r.Name() {
		return ErrUnhandled
	}

	mbox := &imap.MailboxInfo{}
	if err := mbox.Parse(fields); err != nil {
		return err
	}

	r.Mailboxes <- mbox
	return nil
}

func intersection(s1, s2 []string) (inter []string) {
    hash := make(map[string]bool)
    for _, e := range s1 {
        hash[e] = true
    }
    for _, e := range s2 {
        // If elements present in the hashmap then append intersection list.
        if hash[e] {
            inter = append(inter, e)
        }
    }
    //Remove dups from slice.
    inter = removeDups(inter)
    return
}

//Remove dups from slice.
func removeDups(elements []string)(nodups []string) {
    encountered := make(map[string]bool)
    for _, element := range elements {
        if !encountered[element] {
            nodups = append(nodups, element)
            encountered[element] = true
        }
    }
    return
}

var specialuse = []string{"\\ALL", "\\Archive", "\\Drafts", "\\Flagged", "\\Junk", "\\Sent", "\\Trash", "\\Important"}

func (r *List) WriteTo(w *imap.Writer) error {
	respName := r.Name()

	for mbox := range r.Mailboxes {
		if r.SpecialUse && len(intersection(mbox.Attributes, specialuse)) == 0 {
			continue
		}
		fields := []interface{}{imap.RawString(respName)}
		fields = append(fields, mbox.Format()...)

		resp := imap.NewUntaggedResp(fields)
		if err := resp.WriteTo(w); err != nil {
			return err
		}
	}

	return nil
}
