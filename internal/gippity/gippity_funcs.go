package gippity

// import gbp_wttrin "github.com/toksikk/gbp-wttrin/plugin"

// StoredFunction is a struct to store a function with a description and parameters for OpenAI completion
type StoredFunction struct {
	Description string
	Parameters  string
	Function    func()
}

var functionRegistry = make(map[string]StoredFunction)

// RegisterFunction registers a function to be called by a completion
func RegisterFunction(name string, description string, parameters string, fn StoredFunction) {
	functionRegistry[name] = StoredFunction{
		Description: description,
		Parameters:  parameters,
		Function:    fn.Function,
	}
}

// func init() {
// 	RegisterFunction("wttrinStart", "Start the wttrin plugin", "discord *discordgo.Session", gbp_wttrin.Start)
// }
