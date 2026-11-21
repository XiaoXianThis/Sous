#import <Foundation/Foundation.h>
#import <dispatch/dispatch.h>
#import <LocalAuthentication/LocalAuthentication.h>

// Use the system's device-owner policy, not the biometrics-only policy.
// The system can offer the Mac login password when Touch ID isn't usable.
int sousAuthenticateOwner(void) {
    @autoreleasepool {
        LAContext *ctx = [[LAContext alloc] init];
        if (![ctx canEvaluatePolicy:LAPolicyDeviceOwnerAuthentication error:NULL]) {
            [ctx release];
            return 0;
        }
        dispatch_semaphore_t complete = dispatch_semaphore_create(0);
        __block int verified = 0;
        [ctx evaluatePolicy:LAPolicyDeviceOwnerAuthentication localizedReason:@"解锁 Sous 本机工作区" reply:^(BOOL success, NSError *error) {
            verified = success ? 1 : 0;
            dispatch_semaphore_signal(complete);
        }];
        dispatch_semaphore_wait(complete, DISPATCH_TIME_FOREVER);
        [ctx release];
        dispatch_release(complete);
        return verified;
    }
}
