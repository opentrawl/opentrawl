package addressbook

import _ "embed"

//go:embed queries/email_addresses.sql
var emailAddressesSQL string

//go:embed queries/phone_numbers.sql
var phoneNumbersSQL string

//go:embed queries/table_exists.sql
var tableExistsSQL string
