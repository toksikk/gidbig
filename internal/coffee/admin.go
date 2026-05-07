package coffee

import "errors"

// AdminGetBeveragePreference returns the stored beverage preference for one user.
func AdminGetBeveragePreference(userID string) (*UserBeveragePreference, bool) {
	d := getDB()
	if d == nil {
		return nil, false
	}
	var pref UserBeveragePreference
	if err := d.Where("user_id = ?", userID).First(&pref).Error; err != nil {
		return nil, false
	}
	return &pref, true
}

// AdminGetAllBeveragePreferences returns all stored beverage preferences.
func AdminGetAllBeveragePreferences() ([]UserBeveragePreference, error) {
	d := getDB()
	if d == nil {
		return nil, errors.New("store not initialized")
	}
	var prefs []UserBeveragePreference
	result := d.Find(&prefs)
	return prefs, result.Error
}
