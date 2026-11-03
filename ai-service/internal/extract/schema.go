// Package extract is ai-service's document-understanding contract: the doc
// types, the per-type field schemas, the server-owned prompt, and a tolerant
// parser that coerces whatever the model wrote into typed, validated output.
//
// The model is asked for JSON in plain text (no provider JSON mode — support
// varies across OpenAI-compatible providers). Everything it returns is
// untrusted: unknown fields are dropped, values are normalized per kind, and
// a reply that cannot be parsed degrades to docType "other" — never a 500.
package extract

// Kind is how a field's value is validated and normalized.
type Kind int

// Field kinds.
const (
	KindString Kind = iota
	KindStrings
	KindDate
	KindAmount
	KindCurrency
	KindEmails
	KindPhones
	KindURL
	KindCategory
	KindLineItems
	KindKeyValues
)

// Field is one schema entry.
type Field struct {
	Name string
	Kind Kind
	// Hint is the prose the model sees for this field.
	Hint string
}

// DocTypes is the classification enum, in prompt order.
var DocTypes = []string{"invoice", "receipt", "id_document", "business_card", "form", "letter", "contract", "statement", "other"}

// IDSubtypes refine id_document.
var IDSubtypes = []string{"passport", "aadhaar", "pan", "driver_license", "voter_id", "business_license", "other_id"}

// ReceiptCategories is the fixed expense category list; anything else is "other".
var ReceiptCategories = []string{"food", "groceries", "transport", "travel", "lodging", "fuel", "shopping", "utilities", "health", "entertainment", "office", "other"}

// Schemas maps a doc type to its fields. Types absent here carry no fields.
var Schemas = map[string][]Field{
	"receipt": {
		{"merchant", KindString, "store or business name"},
		{"date", KindDate, "purchase date"},
		{"total", KindAmount, "grand total paid"},
		{"tax", KindAmount, "total tax (GST/VAT/sales tax)"},
		{"currency", KindCurrency, "ISO 4217 code, e.g. INR"},
		{"category", KindCategory, "one of: food, groceries, transport, travel, lodging, fuel, shopping, utilities, health, entertainment, office, other"},
		{"paymentMethod", KindString, "cash, card, UPI…"},
	},
	"invoice": {
		{"invoiceNumber", KindString, "invoice number"},
		{"vendor", KindString, "who issued the invoice"},
		{"customer", KindString, "who is billed"},
		{"issueDate", KindDate, "invoice date"},
		{"dueDate", KindDate, "payment due date"},
		{"subtotal", KindAmount, "amount before tax"},
		{"tax", KindAmount, "total tax"},
		{"total", KindAmount, "amount due"},
		{"currency", KindCurrency, "ISO 4217 code"},
		{"lineItems", KindLineItems, "array of {description, quantity, unitPrice, amount}"},
	},
	"business_card": {
		{"name", KindString, "person's full name"},
		{"jobTitle", KindString, "role or designation"},
		{"company", KindString, "company or organisation"},
		{"emails", KindEmails, "array of email addresses"},
		{"phones", KindPhones, "array of phone numbers with country code when shown"},
		{"website", KindURL, "website"},
		{"address", KindString, "postal address on one line"},
	},
	"id_document": {
		{"fullName", KindString, "holder's name as printed"},
		{"documentNumber", KindString, "ID / passport / licence number exactly as printed"},
		{"dateOfBirth", KindDate, "date of birth"},
		{"issueDate", KindDate, "date of issue"},
		{"expiryDate", KindDate, "date of expiry"},
		{"issuingAuthority", KindString, "issuing country, state or authority"},
		{"nationality", KindString, "nationality when printed"},
		{"address", KindString, "address when printed, one line"},
	},
	"form": {
		{"fields", KindKeyValues, "array of {key, value} for every labelled field, in order"},
	},
	"letter": {
		{"sender", KindString, "who wrote it"},
		{"recipient", KindString, "who it is addressed to"},
		{"date", KindDate, "letter date"},
	},
	"contract": {
		{"parties", KindStrings, "array of the parties' names"},
		{"effectiveDate", KindDate, "start / effective date"},
		{"endDate", KindDate, "end or termination date"},
	},
	"statement": {
		{"accountHolder", KindString, "account holder"},
		{"period", KindString, "statement period as printed"},
		{"closingBalance", KindAmount, "closing balance"},
		{"currency", KindCurrency, "ISO 4217 code"},
	},
}

// Limits on what is kept from a reply.
const (
	maxString    = 300
	maxList      = 20
	maxLineItems = 100
	maxKeyValues = 100
	maxTables    = 10
	maxRows      = 200
	maxCols      = 20
	maxCell      = 200
	maxTitle     = 80
)
