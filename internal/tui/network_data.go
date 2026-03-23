package tui

// network_data.go contains network-specific constants, contract data, and
// scan node URLs. These values change when contracts are redeployed or when
// new scan nodes come online — keep them in one place for easy updates.
//
// Testnet utility token contracts were discovered via probe-holdings3.ts
// on 2026-03-18. The scan node registry does not return
// InstrumentConfiguration context for utility tokens, so we provide it
// from hardcoded data.

import "strings"

// ---------------------------------------------------------------------------
// Instrument admins
// ---------------------------------------------------------------------------

// Mainnet instrument admins + utilities URL
const (
	mainnetCBTCAdmin   = "cbtc-network::12205af3b949a04776fc48cdcc05a060f6bda2e470632935f375d1049a8546a3b262"
	mainnetUSDXLRAdmin = "4e667aab-9089-400b-9485-74d73732f068::1220dd489bd2242472a14015e9aebc12a48879a7e3aac60875506df30f7fce4e0f78"
)

// Testnet instrument admins
const (
	testnetCBTCAdmin  = "cbtc-network::12201b1741b63e2494e4214cf0bedc3d5a224da53b3bf4d76dba468f8e97eb15508f"
	testnetUSDCxAdmin = "decentralized-usdc-interchain-rep::122049e2af8a725bd19759320fc83c638e7718973eac189d8f201309c512d1ffec61"
)

// Testnet synchronizer ID (global domain)
const testnetSynchronizer = "global-domain::1220f22a8b8f2d813c25b9a684dc4dd52b532a0174d8e73a13cdf2baabfff7518337"

// ---------------------------------------------------------------------------
// Utilities API URLs
// ---------------------------------------------------------------------------

const (
	mainnetUtilitiesBaseURL = "https://api.utilities.digitalasset.com"
	testnetUtilitiesBaseURL = "https://api.utilities.digitalasset-staging.com"
)

func utilitiesBaseURLForNetwork(network string) string {
	if strings.EqualFold(network, "testnet") {
		return testnetUtilitiesBaseURL
	}
	return mainnetUtilitiesBaseURL
}

// ---------------------------------------------------------------------------
// Utility admin detection
// ---------------------------------------------------------------------------

func isUtilitiesAdmin(admin, network string) bool {
	if strings.EqualFold(network, "testnet") {
		return admin == testnetCBTCAdmin || admin == testnetUSDCxAdmin
	}
	return admin == mainnetCBTCAdmin || admin == mainnetUSDXLRAdmin
}

// ---------------------------------------------------------------------------
// Scan node registries
// ---------------------------------------------------------------------------

var mainnetScanRegistries = []string{
	"https://scan.sv-1.global.canton.network.digitalasset.com",
	"https://scan.sv-2.global.canton.network.digitalasset.com",
	"https://scan.sv-1.global.canton.network.c7.digital",
	"https://scan.sv-1.global.canton.network.cumberland.io",
	"https://scan.sv-1.global.canton.network.fivenorth.io",
	"https://scan.sv-1.global.canton.network.sync.global",
	"https://scan.sv-1.global.canton.network.proofgroup.xyz",
	"https://scan.sv.global.canton.network.sv-nodeops.com",
	"https://scan.sv-1.global.canton.network.tradeweb.com",
}

var testnetScanRegistries = []string{
	"https://scan.sv-1.test.global.canton.network.digitalasset.com",
	"https://scan.sv-2.test.global.canton.network.digitalasset.com",
	"https://scan.sv-1.test.global.canton.network.sync.global",
}

func scanRegistriesForNetwork(network string) []string {
	switch strings.ToLower(network) {
	case "testnet", "devnet":
		return testnetScanRegistries
	default:
		return mainnetScanRegistries
	}
}

// ---------------------------------------------------------------------------
// Hardcoded utility token contracts (testnet only)
// ---------------------------------------------------------------------------

type utilityTokenContract struct {
	ContractID       string
	TemplateID       string
	CreatedEventBlob string
}

type utilityTokenContracts struct {
	AllocationFactory       utilityTokenContract
	InstrumentConfiguration utilityTokenContract
}

var testnetUtilityContracts = map[string]utilityTokenContracts{
	testnetCBTCAdmin: {
		AllocationFactory: utilityTokenContract{
			ContractID:       "00c956e318c52f8aa0b6acebd0bf62cb699df4627b5565fb332c79202479a36bafca111220cc0e27fb3e6fcdfa2cad3b1dd4843348ce60d04f6daacccecab981523620b7da",
			TemplateID:       "170929b11d5f0ed1385f890f42887c31ff7e289c0f4bc482aff193a7173d576c:Utility.Registry.App.V0.Service.AllocationFactory:AllocationFactory",
			CreatedEventBlob: "CgMyLjES+wUKRQDJVuMYxS+KoLas69C/YstpnfRie1Vl+zMseSAkeaNrr8oREiDMDif7Pm/N+iytOx3UhDNIzmDQT22qzM7KuYFSNiC32hIXdXRpbGl0eS1yZWdpc3RyeS1hcHAtdjAajQEKQDE3MDkyOWIxMWQ1ZjBlZDEzODVmODkwZjQyODg3YzMxZmY3ZTI4OWMwZjRiYzQ4MmFmZjE5M2E3MTczZDU3NmMSB1V0aWxpdHkSCFJlZ2lzdHJ5EgNBcHASAlYwEgdTZXJ2aWNlEhFBbGxvY2F0aW9uRmFjdG9yeRoRQWxsb2NhdGlvbkZhY3RvcnkimwJqmAIKVgpUOlJjYnRjLW5ldHdvcms6OjEyMjAxYjE3NDFiNjNlMjQ5NGU0MjE0Y2YwYmVkYzNkNWEyMjRkYTUzYjNiZjRkNzZkYmE0NjhmOGU5N2ViMTU1MDhmClYKVDpSY2J0Yy1uZXR3b3JrOjoxMjIwMWIxNzQxYjYzZTI0OTRlNDIxNGNmMGJlZGMzZDVhMjI0ZGE1M2IzYmY0ZDc2ZGJhNDY4ZjhlOTdlYjE1NTA4ZgpmCmQ6YkRpZ2l0YWxBc3NldC1VdGlsaXR5T3BlcmF0b3I6OjEyMjAyNjc5ZjJiYmU1N2Q4Y2JhOWVmM2NlZTg0N2FjODIzOWRmMDg3NzEwNWFiMWYwMWE3N2Q0NzQ3N2ZkY2UxMjA0KlJjYnRjLW5ldHdvcms6OjEyMjAxYjE3NDFiNjNlMjQ5NGU0MjE0Y2YwYmVkYzNkNWEyMjRkYTUzYjNiZjRkNzZkYmE0NjhmOGU5N2ViMTU1MDhmMmJEaWdpdGFsQXNzZXQtVXRpbGl0eU9wZXJhdG9yOjoxMjIwMjY3OWYyYmJlNTdkOGNiYTllZjNjZWU4NDdhYzgyMzlkZjA4NzcxMDVhYjFmMDFhNzdkNDc0NzdmZGNlMTIwNDnmBIZo+kEGAEIqCiYKJAgBEiA28r69uEQ4mN310dzSIOTHLYgxmVinL9FuA/XwQLfheRAe",
		},
		InstrumentConfiguration: utilityTokenContract{
			ContractID:       "00dcac0a5a71d77dbfa978ea73f9c5b72daf9d26ba0e9b1d8584190358c94581f8ca111220c183c4851805be4cb8d5c5a2f37c6394aaf95400982453d0584c1fdf6c4950ce",
			TemplateID:       "ed73d5b9ab717333f3dbd122de7be3156f8bf2614a67360c3dd61fc0135133fa:Utility.Registry.V0.Configuration.Instrument:InstrumentConfiguration",
			CreatedEventBlob: "CgMyLjESlwgKRQDcrApacdd9v6l46nP5xbctr50mug6bHYWEGQNYyUWB+MoREiDBg8SFGAW+TLjVxaLzfGOUqvlUAJgkU9BYTB/fbElQzhITdXRpbGl0eS1yZWdpc3RyeS12MBqNAQpAZWQ3M2Q1YjlhYjcxNzMzM2YzZGJkMTIyZGU3YmUzMTU2ZjhiZjI2MTRhNjczNjBjM2RkNjFmYzAxMzUxMzNmYRIHVXRpbGl0eRIIUmVnaXN0cnkSAlYwEg1Db25maWd1cmF0aW9uEgpJbnN0cnVtZW50GhdJbnN0cnVtZW50Q29uZmlndXJhdGlvbiK7BGq4BApmCmQ6YkRpZ2l0YWxBc3NldC1VdGlsaXR5T3BlcmF0b3I6OjEyMjAyNjc5ZjJiYmU1N2Q4Y2JhOWVmM2NlZTg0N2FjODIzOWRmMDg3NzEwNWFiMWYwMWE3N2Q0NzQ3N2ZkY2UxMjA0ClYKVDpSY2J0Yy1uZXR3b3JrOjoxMjIwMWIxNzQxYjYzZTI0OTRlNDIxNGNmMGJlZGMzZDVhMjI0ZGE1M2IzYmY0ZDc2ZGJhNDY4ZjhlOTdlYjE1NTA4ZgpWClQ6UmNidGMtbmV0d29yazo6MTIyMDFiMTc0MWI2M2UyNDk0ZTQyMTRjZjBiZWRjM2Q1YTIyNGRhNTNiM2JmNGQ3NmRiYTQ2OGY4ZTk3ZWIxNTUwOGYKhAEKgQFqfwpWClQ6UmNidGMtbmV0d29yazo6MTIyMDFiMTc0MWI2M2UyNDk0ZTQyMTRjZjBiZWRjM2Q1YTIyNGRhNTNiM2JmNGQ3NmRiYTQ2OGY4ZTk3ZWIxNTUwOGYKCAoGQgRDQlRDChsKGUIXUmVnaXN0cmFySW50ZXJuYWxTY2hlbWUKigEKhwFahAEKgQFqfwpWClQ6UmNidGMtbmV0d29yazo6MTIyMDFiMTc0MWI2M2UyNDk0ZTQyMTRjZjBiZWRjM2Q1YTIyNGRhNTNiM2JmNGQ3NmRiYTQ2OGY4ZTk3ZWIxNTUwOGYKCAoGQgRDQlRDChsKGUIXUmVnaXN0cmFySW50ZXJuYWxTY2hlbWUKBAoCWgAKBAoCWgAqUmNidGMtbmV0d29yazo6MTIyMDFiMTc0MWI2M2UyNDk0ZTQyMTRjZjBiZWRjM2Q1YTIyNGRhNTNiM2JmNGQ3NmRiYTQ2OGY4ZTk3ZWIxNTUwOGYyYkRpZ2l0YWxBc3NldC1VdGlsaXR5T3BlcmF0b3I6OjEyMjAyNjc5ZjJiYmU1N2Q4Y2JhOWVmM2NlZTg0N2FjODIzOWRmMDg3NzEwNWFiMWYwMWE3N2Q0NzQ3N2ZkY2UxMjA0OeYEhmj6QQYAQioKJgokCAESIIFZLFl5oc2bXlpAQxjdZkgA1P4j2ga+zJIx0DdseGTYEB4=",
		},
	},
	testnetUSDCxAdmin: {
		AllocationFactory: utilityTokenContract{
			ContractID:       "00bede5fa4fd3d4f7ac250facd513ded42859076c2890a62b6ad148634c2fee59cca1112200101f0642c657e51f7f4a41e50502167fbbb4ea7b1d5552b9e6e1ab81b457d86",
			TemplateID:       "8c335bb7d522489d71faf3eef046ad1a56f091b55b4f2d3086c7266afca1d647:Utility.Registry.App.V0.Service.AllocationFactory:AllocationFactory",
			CreatedEventBlob: "CgMyLjES/wYKRQC+3l+k/T1PesJQ+s1RPe1ChZB2wokKYratFIY0wv7lnMoREiABAfBkLGV+Uff0pB5QUCFn+7tOp7HVVSuebhq4G0V9hhIXdXRpbGl0eS1yZWdpc3RyeS1hcHAtdjAajQEKQDhjMzM1YmI3ZDUyMjQ4OWQ3MWZhZjNlZWYwNDZhZDFhNTZmMDkxYjU1YjRmMmQzMDg2YzcyNjZhZmNhMWQ2NDcSB1V0aWxpdHkSCFJlZ2lzdHJ5EgNBcHASAlYwEgdTZXJ2aWNlEhFBbGxvY2F0aW9uRmFjdG9yeRoRQWxsb2NhdGlvbkZhY3RvcnkiswJqsAIKWQpXOlVCcmlkZ2UtT3BlcmF0b3I6OjEyMjA5ZDAxMWNlMjUwZGU0MzlmZWZjMzVkMTZkMWFiOWQ1NmZiOTljY2IyNGMxOGQ3OThlZmIyMjM1MmQ1MzNiY2RiCmsKaTpnZGVjZW50cmFsaXplZC11c2RjLWludGVyY2hhaW4tcmVwOjoxMjIwNDllMmFmOGE3MjViZDE5NzU5MzIwZmM4M2M2MzhlNzcxODk3M2VhYzE4OWQ4ZjIwMTMwOWM1MTJkMWZmZWM2MQpmCmQ6YkRpZ2l0YWxBc3NldC1VdGlsaXR5T3BlcmF0b3I6OjEyMjAyNjc5ZjJiYmU1N2Q4Y2JhOWVmM2NlZTg0N2FjODIzOWRmMDg3NzEwNWFiMWYwMWE3N2Q0NzQ3N2ZkY2UxMjA0KlVCcmlkZ2UtT3BlcmF0b3I6OjEyMjA5ZDAxMWNlMjUwZGU0MzlmZWZjMzVkMTZkMWFiOWQ1NmZiOTljY2IyNGMxOGQ3OThlZmIyMjM1MmQ1MzNiY2RiKmdkZWNlbnRyYWxpemVkLXVzZGMtaW50ZXJjaGFpbi1yZXA6OjEyMjA0OWUyYWY4YTcyNWJkMTk3NTkzMjBmYzgzYzYzOGU3NzE4OTczZWFjMTg5ZDhmMjAxMzA5YzUxMmQxZmZlYzYxMmJEaWdpdGFsQXNzZXQtVXRpbGl0eU9wZXJhdG9yOjoxMjIwMjY3OWYyYmJlNTdkOGNiYTllZjNjZWU4NDdhYzgyMzlkZjA4NzcxMDVhYjFmMDFhNzdkNDc0NzdmZGNlMTIwNDmUW0iuSUEGAEIqCiYKJAgBEiCzbjLLZHXmAykV4okcMgzmrn1NLufmehZRjSn4JCCisBAe",
		},
		InstrumentConfiguration: utilityTokenContract{
			ContractID:       "00042d3cc4195cd3e03407be8654e36cf1881aeaa771cc8e631f793999fd2df0fdca1112200ae23ea2ffdcf4841e345dde1cbc84cec7576989eeadb2c77e77e63501e6f581",
			TemplateID:       "b4ae77b8c0c7faa8bc8bb048f035dfe3d85d3e36d4bafaa4cf59631ec635ddb2:Utility.Registry.V0.Configuration.Instrument:InstrumentConfiguration",
			CreatedEventBlob: "CgMyLjEStAkKRQAELTzEGVzT4DQHvoZU42zxiBrqp3HMjmMfeTmZ/S3w/coREiAK4j6i/9z0hB40Xd4cvITOx1dpie6tssd+d+Y1Aeb1gRITdXRpbGl0eS1yZWdpc3RyeS12MBqNAQpAYjRhZTc3YjhjMGM3ZmFhOGJjOGJiMDQ4ZjAzNWRmZTNkODVkM2UzNmQ0YmFmYWE0Y2Y1OTYzMWVjNjM1ZGRiMhIHVXRpbGl0eRIIUmVnaXN0cnkSAlYwEg1Db25maWd1cmF0aW9uEgpJbnN0cnVtZW50GhdJbnN0cnVtZW50Q29uZmlndXJhdGlvbiLsBGrpBApmCmQ6YkRpZ2l0YWxBc3NldC1VdGlsaXR5T3BlcmF0b3I6OjEyMjAyNjc5ZjJiYmU1N2Q4Y2JhOWVmM2NlZTg0N2FjODIzOWRmMDg3NzEwNWFiMWYwMWE3N2Q0NzQ3N2ZkY2UxMjA0ClkKVzpVQnJpZGdlLU9wZXJhdG9yOjoxMjIwOWQwMTFjZTI1MGRlNDM5ZmVmYzM1ZDE2ZDFhYjlkNTZmYjk5Y2NiMjRjMThkNzk4ZWZiMjIzNTJkNTMzYmNkYgprCmk6Z2RlY2VudHJhbGl6ZWQtdXNkYy1pbnRlcmNoYWluLXJlcDo6MTIyMDQ5ZTJhZjhhNzI1YmQxOTc1OTMyMGZjODNjNjM4ZTc3MTg5NzNlYWMxODlkOGYyMDEzMDljNTEyZDFmZmVjNjEKmwEKmAFqlQEKawppOmdkZWNlbnRyYWxpemVkLXVzZGMtaW50ZXJjaGFpbi1yZXA6OjEyMjA0OWUyYWY4YTcyNWJkMTk3NTkzMjBmYzgzYzYzOGU3NzE4OTczZWFjMTg5ZDhmMjAxMzA5YzUxMmQxZmZlYzYxCgkKB0IFVVNEQ3gKGwoZQhdSZWdpc3RyYXJJbnRlcm5hbFNjaGVtZQoECgJaAAqMAQqJAVqGAQqDAWqAAQpZClc6VUJyaWRnZS1PcGVyYXRvcjo6MTIyMDlkMDExY2UyNTBkZTQzOWZlZmMzNWQxNmQxYWI5ZDU2ZmI5OWNjYjI0YzE4ZDc5OGVmYjIyMzUyZDUzM2JjZGIKIwohWh8KHWobCg4KDEIKSXNJc3N1ZXJPZgoJCgdCBVVTREN4CgQKAloAKlVCcmlkZ2UtT3BlcmF0b3I6OjEyMjA5ZDAxMWNlMjUwZGU0MzlmZWZjMzVkMTZkMWFiOWQ1NmZiOTljY2IyNGMxOGQ3OThlZmIyMjM1MmQ1MzNiY2RiKmdkZWNlbnRyYWxpemVkLXVzZGMtaW50ZXJjaGFpbi1yZXA6OjEyMjA0OWUyYWY4YTcyNWJkMTk3NTkzMjBmYzgzYzYzOGU3NzE4OTczZWFjMTg5ZDhmMjAxMzA5YzUxMmQxZmZlYzYxMmJEaWdpdGFsQXNzZXQtVXRpbGl0eU9wZXJhdG9yOjoxMjIwMjY3OWYyYmJlNTdkOGNiYTllZjNjZWU4NDdhYzgyMzlkZjA4NzcxMDVhYjFmMDFhNzdkNDc0NzdmZGNlMTIwNDmUW0iuSUEGAEIqCiYKJAgBEiB22c8ZP/kGXXsITRrlviqwHjLXxwQvBCjqh0oYm2E5gRAe",
		},
	},
}

// buildHardcodedUtilityFactory returns a registryFactoryResult using hardcoded
// contract data for testnet utility tokens (CBTC, USDCx). Returns nil if the
// admin is not a known testnet utility token.
func buildHardcodedUtilityFactory(admin, network string) *registryFactoryResult {
	if !strings.EqualFold(network, "testnet") {
		return nil
	}
	contracts, ok := testnetUtilityContracts[admin]
	if !ok {
		return nil
	}
	return &registryFactoryResult{
		FactoryID: contracts.AllocationFactory.ContractID,
		ChoiceContextData: map[string]any{
			"values": map[string]any{
				"utility.digitalasset.com/instrument-configuration": map[string]any{
					"tag":   "AV_ContractId",
					"value": contracts.InstrumentConfiguration.ContractID,
				},
				"utility.digitalasset.com/sender-credentials": map[string]any{
					"tag":   "AV_List",
					"value": []any{},
				},
			},
		},
		DisclosedContracts: []any{
			map[string]any{
				"templateId":       contracts.AllocationFactory.TemplateID,
				"contractId":       contracts.AllocationFactory.ContractID,
				"createdEventBlob": contracts.AllocationFactory.CreatedEventBlob,
				"synchronizerId":   testnetSynchronizer,
			},
			map[string]any{
				"templateId":       contracts.InstrumentConfiguration.TemplateID,
				"contractId":       contracts.InstrumentConfiguration.ContractID,
				"createdEventBlob": contracts.InstrumentConfiguration.CreatedEventBlob,
				"synchronizerId":   testnetSynchronizer,
			},
		},
	}
}
