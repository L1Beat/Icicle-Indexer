package registrysyncer

// ChainRegistry represents the top-level chain.json from l1beat-l1-registry
type ChainRegistry struct {
	SubnetID    string        `json:"subnetId"`
	Network     string        `json:"network"`
	IsL1        bool          `json:"isL1"`
	Categories  []string      `json:"categories"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Logo        string        `json:"logo"`
	Website     string        `json:"website"`
	Socials     []Social      `json:"socials"`
	Chains      []ChainEntry  `json:"chains"`
}

// Social represents a social media link
type Social struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ChainEntry represents a blockchain within the registry entry
type ChainEntry struct {
	BlockchainID        string      `json:"blockchainId"`
	Name                string      `json:"name"`
	Description         string      `json:"description"`
	EvmChainID          uint64      `json:"evmChainId"`
	VmName              string      `json:"vmName"`
	VmID                string      `json:"vmId"`
	RpcURLs             []string    `json:"rpcUrls"`
	ExplorerURL         string      `json:"explorerUrl"`
	SybilResistanceType string      `json:"sybilResistanceType"`
	NativeToken         NativeToken `json:"nativeToken"`
}

// NativeToken represents the native token of a chain
type NativeToken struct {
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Decimals uint8  `json:"decimals"`
	LogoURI  string `json:"logoUri"`
}
